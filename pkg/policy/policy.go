package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Policy struct {
	Name             string           `yaml:"name"`
	Compile          CompileConfig    `yaml:"compile"`
	Run              RunConfig        `yaml:"run"`
	LibraryFiles     []string         `yaml:"library_files"` // .c/.o/.h files from ~/.config/autoscan/libraries/
	TestFiles        []string         `yaml:"test_files,omitempty"`
	ConfigDir        string           `yaml:"-"` // Directory for banned.yaml, libraries, test files, expected outputs
	BannedFunctions  []string         `yaml:"-"` // Loaded from global banned.yaml
	BannedConstructs BannedConstructs `yaml:"-"`
}

type BannedConstructs struct {
	VariableLengthArrays *bool `yaml:"variable_length_arrays"`
	InitializedArrays    *bool `yaml:"initialized_arrays"`
	PthreadAttributes    *bool `yaml:"pthread_attributes"`
}

func (b BannedConstructs) BanVariableLengthArrays() bool {
	return b.VariableLengthArrays == nil || *b.VariableLengthArrays
}
func (b BannedConstructs) BanInitializedArrays() bool {
	return b.InitializedArrays == nil || *b.InitializedArrays
}
func (b BannedConstructs) BanPthreadAttributes() bool {
	return b.PthreadAttributes == nil || *b.PthreadAttributes
}

type GlobalBannedPolicy struct {
	Banned     []string         `yaml:"banned"`
	Constructs BannedConstructs `yaml:"constructs"`
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
	SourceFile string `yaml:"source_file"`
	// InstanceName tells apart several processes built from one source file.
	InstanceName string `yaml:"name,omitempty"`
}

// Name keys the process in scenarios and results: its instance name, or the source file's stem ("S4_client.c" -> "S4_client").
func (p ProcessConfig) Name() string {
	if name := strings.TrimSpace(p.InstanceName); name != "" {
		return name
	}
	return strings.TrimSuffix(filepath.Base(p.SourceFile), ".c")
}

// Binary is the compiled program's path relative to the submission's build dir; instances of one source share it.
func (p ProcessConfig) Binary() string {
	return strings.TrimSuffix(p.SourceFile, ".c")
}

type MultiProcessScenario struct {
	Name            string              `yaml:"name"`
	ProcessArgs     map[string][]string `yaml:"process_args,omitempty"`
	ProcessInputs   map[string]string   `yaml:"process_inputs,omitempty"`
	ProcessDelays   map[string]int      `yaml:"process_delays,omitempty"` // start delay per process, ms
	ExpectedOutputs map[string]string   `yaml:"expected_outputs,omitempty"`
}

type TestCase struct {
	Name  string   `yaml:"name"`
	Args  []string `yaml:"args"`
	Input string   `yaml:"input"`
	// ProducedFile, when set, compares this file's contents against ExpectedOutputFile instead of stdout.
	ProducedFile       string `yaml:"produced_file,omitempty"`
	ExpectedOutputFile string `yaml:"expected_output_file,omitempty"`
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

	if configDir, err := ConfigDir(); err == nil {
		p.ConfigDir = configDir
	}
	if p.Compile.GCC == "" {
		p.Compile.GCC = "gcc"
	}
	if err := p.Run.MultiProcess.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *MultiProcessConfig) validate() error {
	if m == nil {
		return nil
	}
	seen := make(map[string]bool, len(m.Executables))
	for _, proc := range m.Executables {
		name := proc.Name()
		if seen[name] {
			return fmt.Errorf("policy: two processes are named %q; give each instance of a source file its own name", name)
		}
		seen[name] = true
	}
	return nil
}

func LoadWithGlobalsFromConfigDir(path, configDir string) (*Policy, error) {
	p, err := Load(path)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(configDir) != "" {
		p.ConfigDir = configDir
	}

	bannedFile := filepath.Join(p.EffectiveConfigDir(), "banned.yaml")
	banned, err := LoadGlobalBannedPolicy(bannedFile)
	if err != nil {
		return nil, err
	}

	p.BannedFunctions = banned.Banned
	p.BannedConstructs = banned.Constructs
	return p, nil
}

// ConfigDir returns ~/.config/autoscan.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "autoscan"), nil
}

func (p *Policy) EffectiveConfigDir() string {
	if p != nil && strings.TrimSpace(p.ConfigDir) != "" {
		return p.ConfigDir
	}
	configDir, err := ConfigDir()
	if err != nil {
		return filepath.Join(".", ".autoscan")
	}
	return configDir
}

func (p *Policy) BannedSet() map[string]struct{} {
	set := make(map[string]struct{}, len(p.BannedFunctions))
	for _, fn := range p.BannedFunctions {
		set[fn] = struct{}{}
	}
	return set
}

// BuildGCCArgs orders args: compiler flags -> sources -> library .c/.o files -> linker -l flags -> output.
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
	config, err := LoadGlobalBannedPolicy(path)
	return config.Banned, err
}

func LoadGlobalBannedPolicy(path string) (GlobalBannedPolicy, error) {
	var bannedConfig GlobalBannedPolicy
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return bannedConfig, nil
		}
		return bannedConfig, err
	}

	if err := yaml.Unmarshal(data, &bannedConfig); err != nil {
		return bannedConfig, fmt.Errorf("parsing banned.yaml: %w", err)
	}

	return bannedConfig, nil
}
