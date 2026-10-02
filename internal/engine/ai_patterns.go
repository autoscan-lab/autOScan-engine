package engine

import (
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	sitter "github.com/smacker/go-tree-sitter"
)

const (
	tellKindComplexCondition = "complex_condition"
	tellKindWrappedCall      = "wrapped_call"
)

func booleanOperatorCount(node *sitter.Node) int {
	if node == nil {
		return 0
	}
	count := 0
	if node.Type() == "binary_expression" {
		if op := node.ChildByFieldName("operator"); op != nil && (op.Type() == "&&" || op.Type() == "||") {
			count++
		}
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		count += booleanOperatorCount(node.NamedChild(i))
	}
	return count
}

func foldedCallText(node *sitter.Node, content []byte) string {
	var text strings.Builder
	offset := node.StartByte()
	var visit func(*sitter.Node)
	visit = func(child *sitter.Node) {
		if child.Type() == "comment" {
			text.Write(content[offset:child.StartByte()])
			text.WriteByte(' ')
			offset = child.EndByte()
			return
		}
		for i := 0; i < int(child.NamedChildCount()); i++ {
			visit(child.NamedChild(i))
		}
	}
	visit(node)
	text.Write(content[offset:node.EndByte()])
	return strings.Join(strings.Fields(text.String()), " ")
}

// These requested code patterns suggest review, without asserting authorship.
func codePatternTells(file string, content []byte, root *sitter.Node) ([]domain.AITell, map[string]int) {
	var tells []domain.AITell
	counts := map[string]int{}
	add := func(kind, label string, node *sitter.Node) {
		counts[kind]++
		if len(tells) >= 64 {
			return
		}
		snippet := []rune(strings.Join(strings.Fields(node.Content(content)), " "))
		if len(snippet) > 160 {
			snippet = append(snippet[:159], '…')
		}
		tells = append(tells, domain.AITell{Flagged: true, Kind: kind, Label: label, File: file,
			Line: int(node.StartPoint().Row) + 1, EndLine: int(node.EndPoint().Row) + 1, Snippet: string(snippet)})
	}
	for _, pattern := range declarationPatterns(root, content) {
		add(pattern.kind, pattern.label, pattern.node)
	}
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if insideMacro(node) {
			return
		}
		switch node.Type() {
		case "call_expression":
			arguments := node.ChildByFieldName("arguments")
			argumentCount := 0
			if arguments != nil {
				for i := 0; i < int(arguments.NamedChildCount()); i++ {
					if arguments.NamedChild(i).Type() != "comment" {
						argumentCount++
					}
				}
			}
			if arguments != nil && arguments.EndPoint().Row > arguments.StartPoint().Row && argumentCount >= 2 &&
				len([]rune(foldedCallText(node, content))) >= 100 {
				add(tellKindWrappedCall, "Long function call wrapped across lines", node)
			}
		case "if_statement":
			condition := node.ChildByFieldName("condition")
			if condition != nil && condition.EndPoint().Row-condition.StartPoint().Row >= 2 && booleanOperatorCount(condition) >= 3 {
				add(tellKindComplexCondition, "Complex multiline condition", condition)
			}
		}
		for i := 0; i < int(node.NamedChildCount()); i++ {
			visit(node.NamedChild(i))
		}
	}
	visit(root)
	return tells, counts
}
