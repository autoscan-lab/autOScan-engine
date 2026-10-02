package engine

import (
	"context"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
)

func FingerprintFile(path string, cfg domain.CompareConfig) (domain.FileFingerprint, error) {
	content, err := domain.ReadSourceFile(path)
	if err != nil {
		return domain.FileFingerprint{}, err
	}

	return fingerprintContent(content, cfg.MinFuncTokens)
}

func fingerprintContent(content []byte, minFuncTokens int) (domain.FileFingerprint, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(c.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return domain.FileFingerprint{}, err
	}
	defer tree.Close()

	fp := domain.FileFingerprint{
		Content:     content,
		LineOffsets: buildLineOffsets(content),
	}

	root := tree.RootNode()
	declared := make(map[string]bool)
	collectDeclaredNames(root, content, declared)

	var funcs []*sitter.Node
	collectFunctionDefs(root, &funcs)

	for _, fn := range funcs {
		var tokens []token
		normalizeTokens(fn, content, declared, &tokens)
		if len(tokens) < minFuncTokens {
			continue
		}

		fp.FunctionCount++
		fp.TokenCount += len(tokens)
		if len(fp.Tokens) > 0 {
			fp.Tokens = append(fp.Tokens, 0)
			fp.Spans = append(fp.Spans, domain.Span{})
		}
		for _, t := range tokens {
			fp.Tokens = append(fp.Tokens, hashToken(t.text))
			fp.Spans = append(fp.Spans, domain.Span{Start: t.start, End: t.end})
		}
	}

	return fp, nil
}

type token struct {
	text       string
	start, end uint32
}

func collectFunctionDefs(node *sitter.Node, out *[]*sitter.Node) {
	if node == nil {
		return
	}

	if node.Type() == "function_definition" {
		*out = append(*out, node)
		return
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		collectFunctionDefs(node.Child(i), out)
	}
}

// Parents whose identifier child names something this file declares.
var declaringParents = map[string]bool{
	"init_declarator": true, "function_declarator": true, "pointer_declarator": true,
	"array_declarator": true, "parenthesized_declarator": true, "parameter_declaration": true,
	"declaration": true, "field_declaration": true, "type_definition": true, "enumerator": true,
	"struct_specifier": true, "union_specifier": true, "enum_specifier": true,
}

func collectDeclaredNames(node *sitter.Node, content []byte, declared map[string]bool) {
	switch node.Type() {
	case "preproc_def", "preproc_function_def":
		if name := node.ChildByFieldName("name"); name != nil {
			declared[name.Content(content)] = true
		}
	case "identifier", "field_identifier", "type_identifier":
		if parent := node.Parent(); parent != nil && declaringParents[parent.Type()] {
			declared[node.Content(content)] = true
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		collectDeclaredNames(node.Child(i), content, declared)
	}
}

func normalizeTokens(node *sitter.Node, content []byte, declared map[string]bool, tokens *[]token) {
	if node == nil {
		return
	}

	text := ""
	switch node.Type() {
	case "comment":
		return
	// Literals have child nodes in this grammar, so they collapse here before recursing.
	case "string_literal", "concatenated_string":
		text = "@STR"
	case "char_literal":
		text = "@CHAR"
	default:
		if node.ChildCount() > 0 {
			// Punctuation is dropped, so mark where parameters and the body start; otherwise
			// a header like `int f(int a, int b) { int c;` reads the same as plain declarations.
			if marker := structureMarker(node); marker != "" {
				*tokens = append(*tokens, token{text: marker, start: node.StartByte(), end: node.StartByte() + 1})
			}
			for i := 0; i < int(node.ChildCount()); i++ {
				normalizeTokens(node.Child(i), content, declared, tokens)
			}
			return
		}
		text = normalizeToken(node, content, declared)
	}

	if text != "" {
		*tokens = append(*tokens, token{text: text, start: node.StartByte(), end: node.EndByte()})
	}
}

func structureMarker(node *sitter.Node) string {
	parent := node.Parent()
	if parent == nil {
		return ""
	}
	switch {
	case node.Type() == "parameter_list" && parent.Type() == "function_declarator":
		return "@PARAMS"
	case node.Type() == "compound_statement" && parent.Type() == "function_definition":
		return "@BODY"
	}
	return ""
}

func normalizeToken(node *sitter.Node, content []byte, declared map[string]bool) string {
	raw := strings.TrimSpace(node.Content(content))
	if raw == "" {
		return ""
	}

	if !node.IsNamed() && isPunctuation(raw) {
		return ""
	}

	switch node.Type() {
	// The file's own names are interchangeable; library names (fork, wait, SIGINT) carry meaning.
	case "identifier", "field_identifier", "type_identifier":
		if declared[raw] {
			return "@ID"
		}
		return raw
	case "number_literal":
		return "@NUM"
	default:
		if token, ok := normalizeOperator(raw); ok {
			return token
		}
		return raw
	}
}

// Never zero, which is reserved for function boundaries.
func hashToken(text string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(text))
	return h.Sum64() | 1
}

type tile struct {
	a, b, length int
}

// Greedy String Tiling (as in JPlag): repeatedly marks the longest common unmarked
// runs of at least minMatch tokens, so each token is matched at most once.
func greedyStringTiling(a, b []uint64, minMatch int) []tile {
	if minMatch < 1 {
		minMatch = 1
	}
	positions := make(map[uint64][]int)
	for j, t := range b {
		if t != 0 {
			positions[t] = append(positions[t], j)
		}
	}

	markedA := make([]bool, len(a))
	markedB := make([]bool, len(b))
	var tiles []tile
	for {
		maxLen := minMatch
		var found []tile
		for i := 0; i+maxLen <= len(a); i++ {
			if markedA[i] || a[i] == 0 {
				continue
			}
			for _, j := range positions[a[i]] {
				if markedB[j] {
					continue
				}
				k := 0
				for i+k < len(a) && j+k < len(b) && a[i+k] != 0 && a[i+k] == b[j+k] && !markedA[i+k] && !markedB[j+k] {
					k++
				}
				if k > maxLen {
					maxLen = k
					found = found[:0]
				}
				if k == maxLen {
					found = append(found, tile{a: i, b: j, length: k})
				}
			}
		}
		if len(found) == 0 {
			break
		}
		for _, f := range found {
			if isMarked(markedA, f.a, f.length) || isMarked(markedB, f.b, f.length) {
				continue
			}
			for k := 0; k < f.length; k++ {
				markedA[f.a+k] = true
				markedB[f.b+k] = true
			}
			tiles = append(tiles, f)
		}
	}

	sort.Slice(tiles, func(i, j int) bool { return tiles[i].a < tiles[j].a })
	return tiles
}

func isMarked(marked []bool, start, length int) bool {
	for k := start; k < start+length; k++ {
		if marked[k] {
			return true
		}
	}
	return false
}

func tiledTokens(tiles []tile) int {
	total := 0
	for _, t := range tiles {
		total += t.length
	}
	return total
}

func tileSpan(fp domain.FileFingerprint, start, length int) domain.Span {
	return domain.Span{Start: fp.Spans[start].Start, End: fp.Spans[start+length-1].End}
}

func isPunctuation(raw string) bool {
	switch raw {
	case "(", ")", "{", "}", "[", "]", ";", ",", ".", "->":
		return true
	default:
		return false
	}
}

func normalizeOperator(raw string) (string, bool) {
	switch raw {
	case "=", "+=", "-=", "*=", "/=", "%=", "<<=", ">>=", "&=", "|=", "^=":
		return "@ASSIGN", true
	case "++", "--":
		return "@INCDEC", true
	case "+", "-", "*", "/", "%":
		return "@ARITH", true
	case "==", "!=", "<", "<=", ">", ">=":
		return "@CMP", true
	case "&&", "||", "!":
		return "@LOGIC", true
	case "&", "|", "^", "~", "<<", ">>":
		return "@BIT", true
	case "?", ":":
		return "@TERNARY", true
	case "for", "while", "do":
		return "@LOOP", true
	default:
		return "", false
	}
}

func buildLineOffsets(content []byte) []int {
	offsets := []int{0}
	for i, b := range content {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

func byteToLineCol(lineOffsets []int, pos int) (int, int) {
	line := sort.Search(len(lineOffsets), func(i int) bool {
		return lineOffsets[i] > pos
	}) - 1
	if line < 0 {
		line = 0
	}
	col := pos - lineOffsets[line]
	return line + 1, col + 1
}

func extractSnippet(content []byte, start, end, max int) string {
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}
	if start >= end {
		return ""
	}
	raw := strings.ReplaceAll(string(content[start:end]), "\n", " ")
	raw = strings.TrimSpace(raw)
	if len(raw) <= max {
		return raw
	}
	return raw[:max-3] + "..."
}

func convertSpans(fp domain.FileFingerprint, spans []domain.Span) []domain.MatchSpan {
	result := make([]domain.MatchSpan, 0, len(spans))
	for _, sp := range spans {
		startLine, startCol := byteToLineCol(fp.LineOffsets, int(sp.Start))
		endLine, endCol := byteToLineCol(fp.LineOffsets, int(sp.End))
		snippet := extractSnippet(fp.Content, int(sp.Start), int(sp.End), 80)
		result = append(result, domain.MatchSpan{
			StartLine: startLine,
			StartCol:  startCol,
			EndLine:   endLine,
			EndCol:    endCol,
			Snippet:   snippet,
		})
	}
	return result
}
