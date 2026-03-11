package ai

import (
	"fmt"
	"sort"
	"strings"
)

// Dictionary is the AI pattern dictionary loaded from YAML.
type Dictionary struct {
	Entries []Entry `yaml:"entries"`
}

// Entry is one code pattern to compare against submissions.
type Entry struct {
	ID       string `yaml:"id"`
	Category string `yaml:"category"`
	Title    string `yaml:"title"`
	Code     string `yaml:"code"`
}

// ValidationError aggregates schema/quality issues for a dictionary.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return ""
	}
	if len(e.Problems) == 1 {
		return "invalid ai dictionary: " + e.Problems[0]
	}
	return fmt.Sprintf("invalid ai dictionary (%d issues): %s", len(e.Problems), strings.Join(e.Problems, "; "))
}

func (d *Dictionary) Validate() error {
	if d == nil {
		return &ValidationError{Problems: []string{"dictionary is nil"}}
	}

	var problems []string
	if len(d.Entries) == 0 {
		problems = append(problems, "entries must not be empty")
	}

	seen := make(map[string]int, len(d.Entries))
	for i, e := range d.Entries {
		row := i + 1
		id := strings.TrimSpace(e.ID)
		if id == "" {
			problems = append(problems, fmt.Sprintf("entry %d: id is required", row))
		} else {
			seen[id]++
		}

		if strings.TrimSpace(e.Category) == "" {
			problems = append(problems, fmt.Sprintf("entry %d (%q): category is required", row, id))
		}
		if strings.TrimSpace(e.Title) == "" {
			problems = append(problems, fmt.Sprintf("entry %d (%q): title is required", row, id))
		}
		if strings.TrimSpace(e.Code) == "" {
			problems = append(problems, fmt.Sprintf("entry %d (%q): code must not be empty", row, id))
		}
	}

	if len(seen) > 0 {
		ids := make([]string, 0)
		for id, n := range seen {
			if n > 1 {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			problems = append(problems, fmt.Sprintf("duplicate id: %q", id))
		}
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}
