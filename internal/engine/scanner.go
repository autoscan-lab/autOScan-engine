package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
)

// ScanEngine scans source files for banned function calls using tree-sitter.
type ScanEngine struct {
	policy    *policy.Policy
	bannedSet map[string]struct{}
	parser    *sitter.Parser
	lang      *sitter.Language
}

// NewScanEngine creates a new scan engine.
func NewScanEngine(p *policy.Policy) *ScanEngine {
	parser := sitter.NewParser()
	lang := c.GetLanguage()
	parser.SetLanguage(lang)

	return &ScanEngine{
		policy:    p,
		bannedSet: p.BannedSet(),
		parser:    parser,
		lang:      lang,
	}
}

// ScanAll scans all submissions in parallel.
func (e *ScanEngine) ScanAll(submissions []domain.Submission, onComplete func(domain.Submission, domain.ScanResult)) []domain.ScanResult {
	results := make([]domain.ScanResult, len(submissions))

	numWorkers := runtime.NumCPU()
	if numWorkers > len(submissions) {
		numWorkers = len(submissions)
	}
	if numWorkers > 8 {
		numWorkers = 8
	}
	if numWorkers == 0 {
		return results
	}

	jobs := make(chan int, len(submissions))
	for i := range submissions {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Create a parser per worker (tree-sitter parsers aren't thread-safe)
			parser := sitter.NewParser()
			parser.SetLanguage(e.lang)

			for idx := range jobs {
				sub := submissions[idx]
				result := e.scanWithParser(parser, sub)

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

// scanWithParser scans a submission using the provided parser.
func (e *ScanEngine) scanWithParser(parser *sitter.Parser, sub domain.Submission) domain.ScanResult {
	var allHits []domain.BannedHit
	var parseErrors []string

	for _, cFile := range sub.CFiles {
		filePath := filepath.Join(sub.Path, cFile)
		hits, err := e.scanFileWithParser(parser, filePath, cFile)
		if err != nil {
			parseErrors = append(parseErrors, cFile+": "+err.Error())
			continue
		}
		allHits = append(allHits, hits...)
	}

	return domain.NewScanResult(allHits, parseErrors)
}

func (e *ScanEngine) scanFileWithParser(parser *sitter.Parser, filePath, displayName string) ([]domain.BannedHit, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	var hits []domain.BannedHit
	lines := strings.Split(string(content), "\n")
	e.walkTree(tree.RootNode(), content, lines, displayName, &hits)

	return hits, nil
}

func (e *ScanEngine) walkTree(node *sitter.Node, content []byte, lines []string, fileName string, hits *[]domain.BannedHit) {
	if node == nil {
		return
	}

	// Check if this is a call expression
	if node.Type() == "call_expression" {
		e.checkCallExpression(node, content, lines, fileName, hits)
	}

	// Recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		e.walkTree(child, content, lines, fileName, hits)
	}
}

func (e *ScanEngine) checkCallExpression(node *sitter.Node, content []byte, lines []string, fileName string, hits *[]domain.BannedHit) {
	// Get the function being called (first child is usually the function identifier)
	if node.ChildCount() == 0 {
		return
	}

	funcNode := node.Child(0)
	if funcNode == nil {
		return
	}

	var funcName string

	switch funcNode.Type() {
	case "identifier":
		// Direct function call: printf(...)
		funcName = funcNode.Content(content)
	case "field_expression":
		// Member access: obj.method(...) - get the field name
		if funcNode.ChildCount() >= 3 {
			field := funcNode.Child(2) // Usually: object . field
			if field != nil && field.Type() == "field_identifier" {
				funcName = field.Content(content)
			}
		}
	default:
		// Other cases (function pointers, etc.) - skip for now
		return
	}

	if funcName == "" {
		return
	}

	// Check if this function is banned
	if _, banned := e.bannedSet[funcName]; banned {
		line := int(funcNode.StartPoint().Row) + 1 // 1-based
		col := int(funcNode.StartPoint().Column) + 1

		// Get snippet (the line containing the call)
		snippet := ""
		if line-1 < len(lines) {
			snippet = strings.TrimSpace(lines[line-1])
			// Truncate long snippets
			if len(snippet) > 80 {
				snippet = snippet[:77] + "..."
			}
		}

		*hits = append(*hits, domain.NewBannedHit(
			funcName,
			fileName,
			line,
			col,
			snippet,
		))
	}
}
