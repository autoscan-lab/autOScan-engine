package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

const (
	DefaultTimeout = 5 * time.Second
	DefaultWorkers = 0 // 0 = runtime.NumCPU()
)

type CompileEngine struct {
	policy         *policy.Policy
	timeout        time.Duration
	workers        int
	tempDir        string
	outputDir      string
	cachedLibFiles []string
	cachedLibDir   string
	cachedLibWarn  []string
}

type CompileOption func(*CompileEngine)

func WithWorkers(n int) CompileOption {
	return func(e *CompileEngine) { e.workers = n }
}

func WithOutputDir(dir string) CompileOption {
	return func(e *CompileEngine) { e.outputDir = dir }
}

func NewCompileEngine(p *policy.Policy, opts ...CompileOption) (*CompileEngine, error) {
	tempDir, err := os.MkdirTemp("", "autoscan-*")
	if err != nil {
		return nil, err
	}

	e := &CompileEngine{
		policy:  p,
		timeout: DefaultTimeout,
		workers: DefaultWorkers,
		tempDir: tempDir,
	}

	for _, opt := range opts {
		opt(e)
	}

	if e.workers <= 0 {
		e.workers = runtime.NumCPU()
	}

	return e, nil
}

func (e *CompileEngine) Cleanup() error {
	if e.tempDir != "" {
		return os.RemoveAll(e.tempDir)
	}
	return nil
}

func (e *CompileEngine) CompileAll(ctx context.Context, submissions []domain.Submission, onComplete func(domain.Submission, domain.CompileResult)) []domain.CompileResult {
	results := make([]domain.CompileResult, len(submissions))

	jobs := make(chan int, len(submissions))
	for i := range submissions {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for w := 0; w < e.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				sub := submissions[idx]
				result := e.compile(ctx, sub)

				mu.Lock()
				results[idx] = result
				mu.Unlock()

				if onComplete != nil {
					onComplete(sub, result)
				}
			}
		}()
	}

	wg.Wait()
	return results
}

func (e *CompileEngine) compile(ctx context.Context, sub domain.Submission) domain.CompileResult {
	start := time.Now()

	baseDir := e.tempDir
	if e.outputDir != "" {
		baseDir = e.outputDir
	}

	outputDir := filepath.Join(baseDir, sub.ID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return domain.NewCompileResult(false, nil, "", err.Error(), time.Since(start).Milliseconds(), false)
	}

	// Some assignments access companion source files at runtime (e.g. ftok path inputs).
	e.copySourceFiles(sub, outputDir)

	if e.policy.Run.MultiProcess != nil && e.policy.Run.MultiProcess.Enabled && len(e.policy.Run.MultiProcess.Executables) > 0 {
		return e.compileMultiProcess(ctx, sub, outputDir, start)
	}

	if e.policy.Compile.SourceFile == "" {
		return domain.NewCompileResult(false, nil, "", "Policy error: source_file must be set for single-process compilation", time.Since(start).Milliseconds(), false)
	}

	sourceFile := filepath.Join(sub.Path, e.policy.Compile.SourceFile)
	if _, err := os.Stat(sourceFile); err != nil {
		return domain.NewCompileResult(false, nil, "", fmt.Sprintf("Source file not found: %s", e.policy.Compile.SourceFile), time.Since(start).Milliseconds(), false)
	}
	sourceFiles := []string{sourceFile}
	outputName := strings.TrimSuffix(e.policy.Compile.SourceFile, ".c")

	outputPath := filepath.Join(outputDir, outputName)
	libraryFiles, libDir, libraryWarnings := e.resolveLibraryFiles()

	run := e.compileExecutable(ctx, sub, outputDir, sourceFiles, libraryFiles, libDir, outputPath)
	duration := time.Since(start).Milliseconds()

	stderrStr := run.stderr
	if len(libraryWarnings) > 0 {
		warnings := strings.Join(libraryWarnings, "\n") + "\n"
		if stderrStr != "" {
			stderrStr = warnings + "─── Compilation Output ───\n" + stderrStr
		} else {
			stderrStr = warnings
		}
	}

	return domain.NewCompileResult(run.err == nil, run.cmd, run.stdout, stderrStr, duration, run.timedOut)
}

type gccRun struct {
	cmd      []string
	stdout   string
	stderr   string
	err      error
	timedOut bool
}

func (e *CompileEngine) runGCC(ctx context.Context, sub domain.Submission, outputDir, libDir string, args []string) gccRun {
	timeoutCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	gccCmdline := append([]string{e.policy.Compile.GCC}, args...)
	cleanup := func() {}
	if sandboxAvailable() {
		spec := sandboxSpec{workDir: outputDir, readOnly: existingPaths(sub.Path, libDir)}
		gccCmdline, cleanup = sandboxCommand(spec, gccCmdline)
	}
	defer cleanup()

	cmd := exec.CommandContext(timeoutCtx, gccCmdline[0], gccCmdline[1:]...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.Dir = sub.Path
	cmd.Env = MinimalEnv(sub.Path)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return gccRun{
		cmd:      append([]string{e.policy.Compile.GCC}, args...),
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		err:      err,
		timedOut: timeoutCtx.Err() == context.DeadlineExceeded,
	}
}

// Links the policy library objects only when the source includes a library header.
func (e *CompileEngine) compileExecutable(ctx context.Context, sub domain.Submission, outputDir string, sourceFiles, libraryFiles []string, libDir, outputPath string) gccRun {
	libs := libraryFiles
	if !sourcesIncludeLibrary(sourceFiles, libraryFiles) {
		libs = nil
	}

	args := e.policy.BuildGCCArgs(sourceFiles, libs, outputPath)
	if libDir != "" {
		args = append([]string{"-I", libDir}, args...)
	}
	return e.runGCC(ctx, sub, outputDir, libDir, args)
}

// With no headers among the library files there is nothing to detect: always link.
func sourcesIncludeLibrary(sourceFiles, libraryFiles []string) bool {
	var headers []string
	for _, lib := range libraryFiles {
		if strings.HasSuffix(lib, ".h") {
			headers = append(headers, filepath.Base(lib))
		}
	}
	if len(headers) == 0 {
		return true
	}

	for _, src := range sourceFiles {
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		for _, header := range headers {
			pattern := regexp.MustCompile(`(?m)^\s*#\s*include\s*["<]` + regexp.QuoteMeta(header) + `[">]`)
			if pattern.Match(data) {
				return true
			}
		}
	}
	return false
}

func (e *CompileEngine) resolveLibraryFiles() ([]string, string, []string) {
	if len(e.policy.LibraryFiles) == 0 {
		return nil, "", nil
	}

	if e.cachedLibFiles != nil {
		return e.cachedLibFiles, e.cachedLibDir, e.cachedLibWarn
	}

	libDir := filepath.Join(e.policy.EffectiveConfigDir(), "libraries")

	var libraryFiles []string
	var warnings []string

	for _, libFile := range e.policy.LibraryFiles {
		var fullPath string
		if filepath.IsAbs(libFile) {
			fullPath = libFile
		} else {
			fullPath = filepath.Join(libDir, libFile)
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Warning: Library file not found: %s", fullPath))
			continue
		}

		if strings.HasSuffix(fullPath, ".o") {
			if info.Size() == 0 {
				warnings = append(warnings, fmt.Sprintf("Warning: Object file is empty: %s", fullPath))
				continue
			}
			if info.Size() < 50 {
				warnings = append(warnings, fmt.Sprintf("Warning: Object file seems corrupted: %s", fullPath))
			}
		}

		libraryFiles = append(libraryFiles, fullPath)
	}

	e.cachedLibFiles = libraryFiles
	e.cachedLibDir = libDir
	e.cachedLibWarn = warnings

	return libraryFiles, libDir, warnings
}

func (e *CompileEngine) copySourceFiles(sub domain.Submission, outputDir string) {
	for _, cFile := range sub.CFiles {
		src := filepath.Join(sub.Path, cFile)
		dst := filepath.Join(outputDir, cFile)
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(dst, data, 0644)
		}
	}
}

func (e *CompileEngine) compileMultiProcess(ctx context.Context, sub domain.Submission, outputDir string, start time.Time) domain.CompileResult {
	mp := e.policy.Run.MultiProcess
	libraryFiles, libDir, _ := e.resolveLibraryFiles()

	var allStdout, allStderr bytes.Buffer
	var allCmds []string
	allOK := true
	timedOut := false

	for _, proc := range mp.Executables {
		sourceFile := filepath.Join(sub.Path, proc.SourceFile)

		if _, err := os.Stat(sourceFile); err != nil {
			allStderr.WriteString(fmt.Sprintf("Source file not found: %s\n", proc.SourceFile))
			allOK = false
			continue
		}

		binaryName := strings.TrimSuffix(proc.SourceFile, ".c")
		outputPath := filepath.Join(outputDir, binaryName)

		run := e.compileExecutable(ctx, sub, outputDir, []string{sourceFile}, libraryFiles, libDir, outputPath)
		if run.timedOut {
			timedOut = true
		}

		allCmds = append(allCmds, run.cmd...)
		allCmds = append(allCmds, ";")

		if run.stdout != "" {
			allStdout.WriteString(fmt.Sprintf("=== %s ===\n", proc.Name()))
			allStdout.WriteString(run.stdout)
		}
		if run.stderr != "" {
			allStderr.WriteString(fmt.Sprintf("=== %s ===\n", proc.Name()))
			allStderr.WriteString(run.stderr)
		}

		if run.err != nil {
			allOK = false
		}
	}

	return domain.NewCompileResult(allOK, allCmds, allStdout.String(), allStderr.String(), time.Since(start).Milliseconds(), timedOut)
}
