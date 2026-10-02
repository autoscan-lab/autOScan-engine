package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	sitter "github.com/smacker/go-tree-sitter"
)

const (
	tellKindToolFile = "tool_file"
	tellKindChatText = "chat_text"
	tellKindUnicode  = "unicode"

	maxChatTellsPerFile = 5
	maxTellSnippet      = 100
)

// Project files AI coding tools create next to the code they write.
var toolArtifacts = map[string]string{
	".cursor":                 "Cursor project folder",
	".cursorrules":            "Cursor rules file",
	".claude":                 "Claude Code project folder",
	"claude.md":               "Claude Code instructions file",
	"agents.md":               "Coding agent instructions file",
	"gemini.md":               "Gemini CLI instructions file",
	".gemini":                 "Gemini CLI project folder",
	".codex":                  "Codex project folder",
	".windsurf":               "Windsurf project folder",
	".windsurfrules":          "Windsurf rules file",
	".continue":               "Continue project folder",
	".codeium":                "Codeium project folder",
	".clinerules":             "Cline rules",
	".roo":                    "Roo Code project folder",
	".roomodes":               "Roo Code modes file",
	".kiro":                   "Kiro project folder",
	".aider":                  "Aider history or config",
	"copilot-instructions.md": "GitHub Copilot instructions file",
}

func toolArtifactLabel(name string) string {
	lower := strings.ToLower(name)
	if label, ok := toolArtifacts[lower]; ok {
		return label
	}
	if strings.HasPrefix(lower, ".aider.") || lower == ".aiderignore" {
		return toolArtifacts[".aider"]
	}
	return ""
}

// artifactTells looks in the submission folder and its parents up to the student's own top folder.
func artifactTells(sub domain.Submission) []domain.AITell {
	dirs := []string{sub.Path}
	id := filepath.ToSlash(sub.ID)
	path := filepath.ToSlash(sub.Path)
	if strings.HasSuffix(path, "/"+id) {
		top := filepath.FromSlash(strings.TrimSuffix(path, id) + strings.Split(id, "/")[0])
		for dir := sub.Path; dir != top && strings.HasPrefix(dir, top); {
			dir = filepath.Dir(dir)
			dirs = append(dirs, dir)
		}
	}

	var tells []domain.AITell
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			check := []string{entry.Name()}
			if entry.IsDir() && strings.EqualFold(entry.Name(), ".github") {
				if nested, err := os.ReadDir(filepath.Join(dir, entry.Name())); err == nil {
					for _, n := range nested {
						check = append(check, entry.Name()+"/"+n.Name())
					}
				}
			}
			for _, rel := range check {
				label := toolArtifactLabel(filepath.Base(rel))
				full := filepath.Join(dir, rel)
				if label == "" || seen[full] || (filepath.Base(rel) == "copilot-instructions.md" && !strings.Contains(rel, "/")) {
					continue
				}
				seen[full] = true
				tells = append(tells, domain.AITell{Flagged: true, Kind: tellKindToolFile, Label: label, File: displayPath(sub, full)})
			}
		}
	}
	return tells
}

func displayPath(sub domain.Submission, full string) string {
	if rel, err := filepath.Rel(sub.Path, full); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(full)
}

// Generic chat wording is context; explicit model self-identification raises a review flag.
var chatPhrases = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`\bhere(?:'s| is) (?:the|an?|your) (?:updated|complete|full|corrected|fixed|modified|revised|final|improved|refactored)\b`,
	`\bas an ai\b`,
	`\bas a (?:large )?language model\b`,
	`\bkey (?:changes|improvements|fixes)\b`,
	`\bi(?:'ve| have) (?:added|updated|modified|changed|fixed|rewritten|refactored|implemented)\b`,
	`\b(?:let me know if|hope this helps|feel free to)\b`,
	`\bin this (?:updated|revised|corrected|improved) version\b`,
	`\.\.\.\s*(?:existing|rest of(?: the)?|remaining|other) code\b`,
	`\b(?:existing|rest of (?:the )?)code (?:unchanged|remains|stays)\b`,
	`<-+\s*(?:added|changed|fixed|new|updated|modified)\b`,
	`\baqu[ií] (?:tienes|tens)\b`,
	`\bespero que (?:te )?(?:sirva|ayude)\b`,
	`^\s*(?://|/\*|\*)?\s*(?:code\s+)?(?:generated|written|created)\s+(?:by|with)\s+(?:chatgpt|claude|github copilot|copilot|cursor|gemini|codex)\b`,
}, "|"))

var explicitAIWording = regexp.MustCompile(`(?i)\bas (?:an ai (?:assistant|(?:language )?model)|a (?:large )?language model)\b|^\s*(?://|/\*|\*)?\s*(?:code\s+)?(?:generated|written|created)\s+(?:by|with)\s+(?:chatgpt|claude|github copilot|copilot|cursor|gemini|codex)\b`)

type unicodeClass struct {
	label  string
	anyway bool // also counts inside string literals
	match  func(r rune) bool
}

// Typography has many possible sources and never raises a flag by itself.
var unicodeClasses = []unicodeClass{
	{"Invisible character (zero-width or non-breaking space)", true, func(r rune) bool {
		return r == 0x00A0 || (r >= 0x200B && r <= 0x200D) || r == 0x2060 || r == 0xFEFF
	}},
	{"Em or en dash (— –) in a comment", false, func(r rune) bool { return r == 0x2014 || r == 0x2013 }},
	{"Curly quotes in a comment", false, func(r rune) bool { return r >= 0x2018 && r <= 0x201D }},
	{"Ellipsis character (…) in a comment", false, func(r rune) bool { return r == 0x2026 }},
	{"Arrow symbol (→) in a comment", false, func(r rune) bool { return (r >= 0x2190 && r <= 0x21FF) || (r >= 0x27F5 && r <= 0x27FF) }},
	{"Math symbol (≤ ≥ ≠) in a comment", false, func(r rune) bool { return r == 0x2264 || r == 0x2265 || r == 0x2260 }},
	{"Emoji or check mark in a comment", false, func(r rune) bool {
		return (r >= 0x2713 && r <= 0x2718) || r == 0x2705 || r == 0x274C || (r >= 0x1F300 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x26FF)
	}},
}

func sourceTells(file string, content []byte, root *sitter.Node) []domain.AITell {
	var tells []domain.AITell
	lines := strings.Split(string(content), "\n")
	snippet := func(row int) string {
		if row < 0 || row >= len(lines) {
			return ""
		}
		text := strings.TrimSpace(lines[row])
		if len([]rune(text)) > maxTellSnippet {
			text = string([]rune(text)[:maxTellSnippet-1]) + "…"
		}
		return text
	}

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			tells = append(tells, domain.AITell{Kind: tellKindChatText, Label: "Markdown code fence", File: file, Line: i + 1, Snippet: snippet(i)})
			break
		}
	}

	type hit struct {
		row, count int
	}
	unicodeHits := make([]hit, len(unicodeClasses))
	chatTells := 0
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		kind := n.Type()
		inComment := kind == "comment"
		if inComment || kind == "string_literal" || kind == "char_literal" || kind == "system_lib_string" {
			text := n.Content(content)
			row := int(n.StartPoint().Row)
			for offset, r := range text {
				for ci, class := range unicodeClasses {
					if (inComment || class.anyway) && class.match(r) && !(r == 0xFEFF && n.StartByte() == 0 && offset == 0) {
						if unicodeHits[ci].count == 0 {
							unicodeHits[ci].row = row + strings.Count(text[:offset], "\n")
						}
						unicodeHits[ci].count++
					}
				}
			}
			if inComment && chatTells < maxChatTellsPerFile {
				for li, commentLine := range strings.Split(text, "\n") {
					if chatPhrases.MatchString(commentLine) {
						tells = append(tells, domain.AITell{Flagged: explicitAIWording.MatchString(commentLine), Kind: tellKindChatText, Label: "Chat assistant wording in a comment", File: file, Line: row + li + 1, Snippet: snippet(row + li)})
						chatTells++
						break
					}
				}
			}
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			visit(n.Child(i))
		}
	}
	visit(root)

	for ci, h := range unicodeHits {
		if h.count == 0 {
			continue
		}
		label := unicodeClasses[ci].label
		if h.count > 1 {
			label = fmt.Sprintf("%s ×%d", label, h.count)
		}
		tells = append(tells, domain.AITell{Kind: tellKindUnicode, Label: label, File: file, Line: h.row + 1, Snippet: snippet(h.row)})
	}
	sort.SliceStable(tells, func(i, j int) bool { return tells[i].Line < tells[j].Line })
	return tells
}
