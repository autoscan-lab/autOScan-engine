package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Policy struct {
	Name            string        `yaml:"name"`
	Compile         CompileConfig `yaml:"compile"`
	Run             RunConfig     `yaml:"run"`
	LibraryFiles    []string      `yaml:"library_files"` // .c/.o/.h files from ~/.config/autoscan/libraries/
	TestFiles       []string      `yaml:"test_files,omitempty"`
	FilePath        string        `yaml:"-"` // Set after loading
	BannedFunctions []string      `yaml:"-"` // Loaded from global banned.yaml
}

type CompileConfig struct {
	GCC        string   `yaml:"gcc"`
	Flags      []string `yaml:"flags"`
	SourceFile string   `yaml:"source_file,omitempty"`
}

type RunConfig struct {
	TestCases    []TestCase          `yaml:"test_cases"`
	MultiProcess *MultiProcessConfig `yaml:"multi_process,omitempty"`
}

type MultiProcessConfig struct {
	Enabled       bool                   `yaml:"enabled"`
	Executables   []ProcessConfig        `yaml:"executables"`
	TestScenarios []MultiProcessScenario `yaml:"test_scenarios,omitempty"`
}

type ProcessConfig struct {
	Name         string   `yaml:"name"`
	SourceFile   string   `yaml:"source_file"`
	Args         []string `yaml:"args,omitempty"`
	Input        string   `yaml:"input,omitempty"`
	StartDelayMs int      `yaml:"start_delay_ms,omitempty"`
}

type MultiProcessScenario struct {
	Name            string              `yaml:"name"`
	ProcessArgs     map[string][]string `yaml:"process_args,omitempty"`
	ProcessInputs   map[string]string   `yaml:"process_inputs,omitempty"`
	ExpectedExits   map[string]int      `yaml:"expected_exits,omitempty"`
	ExpectedOutputs map[string]string   `yaml:"expected_outputs,omitempty"`
}

type TestCase struct {
	Name               string   `yaml:"name"`
	Args               []string `yaml:"args"`
	Input              string   `yaml:"input"`
	ExpectedExit       *int     `yaml:"expected_exit"`
	ExpectedOutputFile string   `yaml:"expected_output_file,omitempty"`
}

func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading policy file: %w", err)
	}

	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing policy YAML: %w", err)
	}

	p.FilePath = path
	if p.Compile.GCC == "" {
		p.Compile.GCC = "gcc"
	}
	return &p, nil
}

func LoadWithGlobals(path string) (*Policy, error) {
	p, err := Load(path)
	if err != nil {
		return nil, err
	}

	bannedFile, err := BannedFilePath()
	if err != nil {
		return p, nil
	}

	bannedFuncs, err := LoadGlobalBanned(bannedFile)
	if err != nil {
		return nil, err
	}

	p.BannedFunctions = bannedFuncs
	return p, nil
}

// ConfigDir returns ~/.config/autoscan.
func ConfigDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AUTOSCAN_CONFIG_DIR")); override != "" {
		return override, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "autoscan"), nil
}

// BannedFilePath returns ~/.config/autoscan/banned.yaml.
func BannedFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "banned.yaml"), nil
}

// Discover finds all .yaml/.yml policy files in dir and attaches global banned functions.
func Discover(dir string) ([]*Policy, error) {
	var policies []*Policy

	bannedFile, _ := BannedFilePath()
	bannedFuncs, _ := LoadGlobalBanned(bannedFile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading policies directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(dir, name)
		p, err := Load(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", name, err)
			continue
		}

		p.BannedFunctions = bannedFuncs
		policies = append(policies, p)
	}

	return policies, nil
}

func (p *Policy) BannedSet() map[string]struct{} {
	set := make(map[string]struct{}, len(p.BannedFunctions))
	for _, fn := range p.BannedFunctions {
		set[fn] = struct{}{}
	}
	return set
}

// BuildGCCArgs constructs gcc arguments with proper ordering:
// compiler flags -> source files -> library files (.c/.o only) -> linker flags (-l*) -> output
func (p *Policy) BuildGCCArgs(sourceFiles []string, libraryFiles []string, outputPath string) []string {
	var compilerFlags, linkerFlags []string

	for _, flag := range p.Compile.Flags {
		if strings.HasPrefix(flag, "-l") {
			linkerFlags = append(linkerFlags, flag)
		} else {
			compilerFlags = append(compilerFlags, flag)
		}
	}

	args := append([]string{}, compilerFlags...)
	args = append(args, sourceFiles...)

	for _, libFile := range libraryFiles {
		if strings.HasSuffix(libFile, ".c") || strings.HasSuffix(libFile, ".o") {
			args = append(args, libFile)
		}
	}

	args = append(args, linkerFlags...)
	args = append(args, "-o", outputPath)

	return args
}

func LoadGlobalBanned(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var bannedConfig struct {
		Banned []string `yaml:"banned"`
	}
	if err := yaml.Unmarshal(data, &bannedConfig); err != nil {
		return nil, fmt.Errorf("parsing banned.yaml: %w", err)
	}

	return bannedConfig.Banned, nil
}
