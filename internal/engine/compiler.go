package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	shortNames     bool
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

func WithShortNames(enabled bool) CompileOption {
	return func(e *CompileEngine) { e.shortNames = enabled }
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

	dirName := sub.ID
	if e.shortNames {
		if idx := strings.Index(dirName, "_"); idx > 0 {
			dirName = dirName[:idx]
		}
	}

	outputDir := filepath.Join(baseDir, dirName)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return domain.NewCompileResult(false, nil, -1, "", err.Error(), time.Since(start).Milliseconds(), false)
	}

	// Some assignments access companion source files at runtime (e.g. ftok path inputs).
	e.copySourceFiles(sub, outputDir)

	if e.policy.Run.MultiProcess != nil && e.policy.Run.MultiProcess.Enabled && len(e.policy.Run.MultiProcess.Executables) > 0 {
		return e.compileMultiProcess(ctx, sub, outputDir, start)
	}

	if e.policy.Compile.SourceFile == "" {
		return domain.NewCompileResult(false, nil, 1, "", "Policy error: source_file must be set for single-process compilation", time.Since(start).Milliseconds(), false)
	}

	sourceFile := filepath.Join(sub.Path, e.policy.Compile.SourceFile)
	if _, err := os.Stat(sourceFile); err != nil {
		return domain.NewCompileResult(false, nil, 1, "", fmt.Sprintf("Source file not found: %s", e.policy.Compile.SourceFile), time.Since(start).Milliseconds(), false)
	}
	sourceFiles := []string{sourceFile}
	outputName := strings.TrimSuffix(e.policy.Compile.SourceFile, ".c")

	outputPath := filepath.Join(outputDir, outputName)
	libraryFiles, libDir, libraryWarnings := e.resolveLibraryFiles()
	args := e.policy.BuildGCCArgs(sourceFiles, libraryFiles, outputPath)

	if libDir != "" {
		args = append([]string{"-I", libDir}, args...)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, e.policy.Compile.GCC, args...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.Dir = sub.Path

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start).Milliseconds()
	timedOut := timeoutCtx.Err() == context.DeadlineExceeded

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	stderrStr := stderr.String()
	if len(libraryWarnings) > 0 {
		warnings := strings.Join(libraryWarnings, "\n") + "\n"
		if stderrStr != "" {
			stderrStr = warnings + "─── Compilation Output ───\n" + stderrStr
		} else {
			stderrStr = warnings
		}
	}

	fullCmd := append([]string{e.policy.Compile.GCC}, args...)
	return domain.NewCompileResult(err == nil, fullCmd, exitCode, stdout.String(), stderrStr, duration, timedOut)
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
	exitCode := 0
	timedOut := false

	for _, proc := range mp.Executables {
		sourceFile := filepath.Join(sub.Path, proc.SourceFile)

		if _, err := os.Stat(sourceFile); err != nil {
			allStderr.WriteString(fmt.Sprintf("Source file not found: %s\n", proc.SourceFile))
			allOK = false
			exitCode = 1
			continue
		}

		binaryName := strings.TrimSuffix(proc.SourceFile, ".c")
		outputPath := filepath.Join(outputDir, binaryName)

		args := e.policy.BuildGCCArgs([]string{sourceFile}, libraryFiles, outputPath)
		if libDir != "" {
			args = append([]string{"-I", libDir}, args...)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, e.timeout)
		cmd := exec.CommandContext(timeoutCtx, e.policy.Compile.GCC, args...)
		configureProcessGroup(cmd)
		cmd.Cancel = func() error { return killProcessGroup(cmd) }
		cmd.Dir = sub.Path

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		if timeoutCtx.Err() == context.DeadlineExceeded {
			timedOut = true
		}
		cancel()

		fullCmd := append([]string{e.policy.Compile.GCC}, args...)
		allCmds = append(allCmds, fullCmd...)
		allCmds = append(allCmds, ";")

		if stdout.Len() > 0 {
			allStdout.WriteString(fmt.Sprintf("=== %s ===\n", proc.Name))
			allStdout.Write(stdout.Bytes())
		}
		if stderr.Len() > 0 {
			allStderr.WriteString(fmt.Sprintf("=== %s ===\n", proc.Name))
			allStderr.Write(stderr.Bytes())
		}

		if err != nil {
			allOK = false
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
	}

	return domain.NewCompileResult(allOK, allCmds, exitCode, allStdout.String(), allStderr.String(), time.Since(start).Milliseconds(), timedOut)
}
