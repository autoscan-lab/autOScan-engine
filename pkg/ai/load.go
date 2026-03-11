package ai

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadDictionary reads, parses, and validates an AI dictionary YAML file.
func LoadDictionary(path string) (*Dictionary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading ai dictionary: %w", err)
	}
	return ParseDictionary(data)
}

// ParseDictionary parses and validates dictionary content.
func ParseDictionary(data []byte) (*Dictionary, error) {
	var d Dictionary
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parsing ai dictionary YAML: %w", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}
