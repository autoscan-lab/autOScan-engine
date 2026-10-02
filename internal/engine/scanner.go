package engine

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
)

type ScanEngine struct {
	bannedSet  map[string]struct{}
	lang       *sitter.Language
	constructs policy.BannedConstructs
}

func NewScanEngine(p *policy.Policy) *ScanEngine {
	return &ScanEngine{
		bannedSet:  p.BannedSet(),
		lang:       c.GetLanguage(),
		constructs: p.BannedConstructs,
	}
}

func (e *ScanEngine) ScanAll(submissions []domain.Submission) []domain.ScanResult {
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
			// One parser per worker; tree-sitter parsers are not thread-safe.
			parser := sitter.NewParser()
			defer parser.Close()
			parser.SetLanguage(e.lang)

			for idx := range jobs {
				sub := submissions[idx]
				result := e.scanWithParser(parser, sub)

				mu.Lock()
				results[idx] = result
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return results
}

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
	content, err := domain.ReadSourceFile(filePath)
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
	for _, pattern := range declarationPatterns(tree.RootNode(), content) {
		if (pattern.kind == "variable_length_array" && !e.constructs.BanVariableLengthArrays()) ||
			(pattern.kind == "initialized_array" && !e.constructs.BanInitializedArrays()) {
			continue
		}
		row := int(pattern.node.StartPoint().Row)
		snippet := strings.TrimSpace(lines[row])
		if len([]rune(snippet)) > 160 {
			snippet = string([]rune(snippet)[:159]) + "…"
		}
		hits = append(hits, domain.NewBannedHit(pattern.label, displayName, row+1, int(pattern.node.StartPoint().Column)+1, snippet))
	}

	return hits, nil
}

func (e *ScanEngine) walkTree(node *sitter.Node, content []byte, lines []string, fileName string, hits *[]domain.BannedHit) {
	if node == nil {
		return
	}

	if node.Type() == "call_expression" {
		e.checkCallExpression(node, content, lines, fileName, hits)
	}
	if e.constructs.BanPthreadAttributes() && node.Type() == "type_identifier" && node.Content(content) == "pthread_attr_t" && !insideMacro(node) {
		row := int(node.StartPoint().Row)
		*hits = append(*hits, domain.NewBannedHit("pthread_attr_t", fileName, row+1, int(node.StartPoint().Column)+1, strings.TrimSpace(lines[row])))
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		e.walkTree(child, content, lines, fileName, hits)
	}
}

func (e *ScanEngine) checkCallExpression(node *sitter.Node, content []byte, lines []string, fileName string, hits *[]domain.BannedHit) {
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
		funcName = funcNode.Content(content)
	case "field_expression":
		if funcNode.ChildCount() >= 3 {
			field := funcNode.Child(2) // grammar shape: object . field
			if field != nil && field.Type() == "field_identifier" {
				funcName = field.Content(content)
			}
		}
	default:
		return
	}

	if funcName == "" {
		return
	}

	_, banned := e.bannedSet[funcName]
	if banned || (e.constructs.BanPthreadAttributes() && strings.HasPrefix(funcName, "pthread_attr_") && !insideMacro(node)) {
		line := int(funcNode.StartPoint().Row) + 1
		col := int(funcNode.StartPoint().Column) + 1

		snippet := ""
		if line-1 < len(lines) {
			snippet = strings.TrimSpace(lines[line-1])
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
