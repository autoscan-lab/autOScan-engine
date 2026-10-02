package engine

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
)

// Scores are fixed per feature, not relative to the class, so a cohort where many use AI can't normalize it.
type styleRamp struct {
	lower, upper float64
}

func (r styleRamp) score(v float64) float64 {
	return math.Max(0, math.Min(1, (v-r.lower)/(r.upper-r.lower)))
}

const (
	evidenceReview  = "review"
	evidenceContext = "context"
)

type styleFeatureDef struct {
	key, label string
	evidence   string
	weight     float64
	// Maps the value to evidence strength: 0 at the first point, 1 at the second.
	ramp styleRamp
}

// Entropy is context only; formatting cannot establish or suppress authorship evidence.
var (
	featureProseComments = styleFeatureDef{"prose_comments", "Comments written as full sentences", evidenceReview, 2, styleRamp{0.15, 0.7}}
	featureDocHeaders    = styleFeatureDef{"doc_headers", "Functions with a comment above them", evidenceReview, 0.75, styleRamp{0.3, 0.9}}
	featureErrorChecks   = styleFeatureDef{"error_checks", "System calls whose result is checked", evidenceReview, 1.5, styleRamp{0.3, 0.95}}
	featureIdioms        = styleFeatureDef{"defensive_idioms", "Course-specific style markers per 100 lines", evidenceReview, 1.5, styleRamp{0, 14}}
	featureFormatEntropy = styleFeatureDef{"format_entropy", "Formatting entropy", evidenceContext, 0, styleRamp{0.12, 0.3}}
	featureNamingEntropy = styleFeatureDef{"naming_entropy", "Naming convention entropy", evidenceContext, 0, styleRamp{0.4, 0.9}}

	reviewFeatures = []styleFeatureDef{featureProseComments, featureDocHeaders, featureErrorChecks, featureIdioms}
)

const (
	styleFlagScore = 0.6
	// Require enough measured feature families before raising a review flag.
	styleMinWeightUsed = 0.5

	minEligibleComments = 3
	minFunctions        = 3
	minCheckableCalls   = 3
	minNamedStyles      = 4
	minCodeLines        = 30
	minChoiceSamples    = 5
	// One habit repeated on every line is still one habit.
	maxIdiomCount       = 5
	maxFeatureLocations = 64
)

// Calls with inspectable result checks; fork remains course-required context.
var checkableCalls = map[string]bool{
	"write": true, "read": true, "pipe": true, "pipe2": true, "dup": true, "dup2": true,
	"open": true, "malloc": true, "calloc": true, "realloc": true, "strdup": true,
	"sigaction": true, "kill": true, "fopen": true, "asprintf": true, "mkfifo": true, "fdopen": true,
	"wait": true, "waitpid": true,
}

type choiceCounts map[string]int

type styleCounts struct {
	codeLines int
	stats     domain.AISourceStats

	eligibleComments, proseComments int
	functions, documentedFunctions  int
	checkableCalls, checkedCalls    int
	idioms                          map[string]int
	namingStyles                    choiceCounts
	choices                         map[string]choiceCounts
	locations                       map[string][]domain.AISourceEvidence
	locationCounts                  map[string]int
}

func newStyleCounts() *styleCounts {
	return &styleCounts{
		idioms:         map[string]int{},
		namingStyles:   choiceCounts{},
		choices:        map[string]choiceCounts{},
		locations:      map[string][]domain.AISourceEvidence{},
		locationCounts: map[string]int{},
		stats:          domain.AISourceStats{PatternCounts: map[string]int{}},
	}
}

func (s *styleCounts) choose(point, variant string) {
	if s.choices[point] == nil {
		s.choices[point] = choiceCounts{}
	}
	s.choices[point][variant]++
}

// AnalyzeSubmissionStyle measures every .c file of a submission and returns its style report and tells.
func AnalyzeSubmissionStyle(sub domain.Submission) (*domain.AIStyleReport, []domain.AITell) {
	style, tells, _ := analyzeSubmissionStyle(sub)
	return style, tells
}

func analyzeSubmissionStyle(sub domain.Submission) (*domain.AIStyleReport, []domain.AITell, [][]metricToken) {
	counts := newStyleCounts()
	tells := artifactTells(sub)
	var tokens [][]metricToken
	names := append([]string(nil), sub.CFiles...)
	sort.Strings(names)
	for _, name := range names {
		content, err := domain.ReadSourceFile(filepath.Join(sub.Path, name))
		if err != nil {
			continue
		}
		fileTells, fileTokens := analyzeStyleSource(name, content, counts)
		tells = append(tells, fileTells...)
		if len(fileTokens) > 0 {
			tokens = append(tokens, fileTokens)
		}
	}
	if counts.codeLines == 0 {
		return nil, tells, tokens
	}
	return scoreStyle(counts), tells, tokens
}

func analyzeStyleSource(name string, content []byte, counts *styleCounts) ([]domain.AITell, []metricToken) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(c.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, nil
	}
	defer tree.Close()
	root := tree.RootNode()
	tells := sourceTells(name, content, root)
	var attributes []attributeSpan
	if root.HasError() {
		masked, spans := compatibleUnusedAttributes(content)
		if len(spans) == 0 {
			return tells, nil
		}
		compatible, err := parser.ParseCtx(context.Background(), nil, masked)
		if err != nil {
			return tells, nil
		}
		defer compatible.Close()
		if compatible.RootNode().HasError() {
			return tells, nil
		}
		root, attributes = compatible.RootNode(), spans
	}
	patterns, patternCounts := codePatternTells(name, content, root)
	tells = append(tells, patterns...)
	for kind, count := range patternCounts {
		counts.stats.PatternCounts[kind] += count
	}

	a := &styleAnalysis{file: name, content: content, counts: counts, header: headerEnd(root), signalHandlers: registeredSignalHandlers(root, content)}
	a.countLines()
	a.walk(root)
	a.comments()
	for _, attribute := range attributes {
		a.counts.idioms["unused attributes"]++
		a.evidence("defensive_idioms", "unused attributes", strings.Count(string(content[:attribute.start]), "\n")+1,
			strings.Count(string(content[:attribute.end]), "\n")+1, string(content[attribute.start:attribute.end]))
	}
	tokens := lexicalTokens(root, content, name)
	counts.stats.TokenCount += len(tokens)
	return tells, tokens
}

type styleAnalysis struct {
	file           string
	signalHandlers map[string]bool
	content        []byte
	counts         *styleCounts
	// Byte offset where the leading comment block (names and logins) ends.
	header      uint32
	commentList []*sitter.Node
}

func (a *styleAnalysis) evidence(key, label string, start, end int, text string) {
	a.counts.locationCounts[key]++
	if len(a.counts.locations[key]) >= maxFeatureLocations {
		return
	}
	snippet := []rune(strings.Join(strings.Fields(text), " "))
	if len(snippet) > 160 {
		snippet = append(snippet[:159], '…')
	}
	a.counts.locations[key] = append(a.counts.locations[key], domain.AISourceEvidence{
		File: a.file, StartLine: start, EndLine: end, Label: label, Snippet: string(snippet),
	})
}

func (a *styleAnalysis) nodeEvidence(key, label string, node *sitter.Node) {
	a.evidence(key, label, int(node.StartPoint().Row)+1, int(node.EndPoint().Row)+1, node.Content(a.content))
}

func (a *styleAnalysis) idiom(label string, node *sitter.Node) {
	a.counts.idioms[label]++
	a.nodeEvidence("defensive_idioms", label, node)
}

func headerEnd(root *sitter.Node) uint32 {
	end := uint32(0)
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "comment" {
			break
		}
		end = child.EndByte()
	}
	return end
}

func (a *styleAnalysis) countLines() {
	lines := strings.Split(string(a.content), "\n")
	blankRun := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blankRun++
			continue
		}
		if blankRun > 0 {
			a.counts.choose("blank_lines", map[bool]string{true: "single", false: "multiple"}[blankRun == 1])
			blankRun = 0
		}
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			a.counts.choose("trailing_space", "dirty")
		} else {
			a.counts.choose("trailing_space", "clean")
		}
		if indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]; indent != "" {
			hasTab, hasSpace := strings.Contains(indent, "\t"), strings.Contains(indent, " ")
			switch {
			case hasTab && hasSpace:
				a.counts.choose("indent_char", "mixed")
			case hasTab:
				a.counts.choose("indent_char", "tab")
			default:
				a.counts.choose("indent_char", "space")
			}
		}
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
			a.counts.codeLines++
		}
	}
}

func (a *styleAnalysis) walk(node *sitter.Node) {
	if node.Type() == "preproc_def" {
		if value := node.ChildByFieldName("value"); value != nil && temporaryPathLiteral.MatchString(strings.TrimSpace(value.Content(a.content))) {
			a.idiom("literal /tmp paths", value)
		}
		return
	}
	if insideMacro(node) {
		return
	}
	a.courseDeclaration(node)
	if (node.Type() == "parameter_declaration" || node.Type() == "field_declaration") && signalStateDeclaration(node, a.content) {
		a.idiom("volatile/sig_atomic_t declarations", node)
	}
	if node.Type() == "declaration" || node.Type() == "type_definition" {
		if signalStateDeclaration(node, a.content) {
			a.idiom("volatile/sig_atomic_t declarations", node)
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.FieldNameForChild(i) != "declarator" {
				continue
			}
			for _, part := range declaratorChain(node.Child(i)) {
				if part.Type() == "array_declarator" {
					a.counts.stats.Arrays++
					break
				}
			}
		}
	}
	switch node.Type() {
	case "string_literal":
		if temporaryPathLiteral.MatchString(node.Content(a.content)) {
			a.idiom("literal /tmp paths", node)
		}
	case "comment":
		a.commentList = append(a.commentList, node)
		return
	case "function_definition":
		a.function(node)
	case "call_expression":
		a.counts.stats.Calls++
		a.call(node)
	case "binary_expression", "assignment_expression":
		a.operatorSpacing(node.ChildByFieldName("operator"))
		if node.Type() == "binary_expression" {
			a.binaryIdioms(node)
		}
	case "init_declarator":
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.Child(i).Type() == "=" {
				a.operatorSpacing(node.Child(i))
			}
		}
		a.declaredName(node)
		if zeroInitializedBuffer(node, a.content) {
			a.idiom("zero-initialized buffers", node)
		}
	case "argument_list":
		if alignedWrappedArguments(node) {
			a.idiom("wrapped arguments aligned to the parenthesis", node)
		}
	case "parameter_declaration":
		a.declaredName(node)
		for _, part := range declaratorChain(node.ChildByFieldName("declarator")) {
			if part.Type() == "array_declarator" {
				a.idiom("array parameters", node)
				break
			}
		}
	case "type_qualifier":
		if node.Content(a.content) == "const" {
			label := "const declarations"
			for owner := node.Parent(); owner != nil; owner = owner.Parent() {
				if owner.Type() == "parameter_declaration" {
					label = "const parameters"
					if kind := owner.ChildByFieldName("type"); kind != nil && kind.Content(a.content) == "void" {
						for _, part := range declaratorChain(owner.ChildByFieldName("declarator")) {
							if part.Type() == "pointer_declarator" {
								label = "const void pointer parameters"
							}
						}
					}
					break
				}
				if owner.Type() == "function_definition" {
					label = "const-qualified function returns"
					break
				}
				if owner.Type() == "declaration" {
					for i := 0; i < int(owner.ChildCount()); i++ {
						if owner.FieldNameForChild(i) == "declarator" && functionDeclarator(owner.Child(i)) != nil {
							label = "const-qualified function returns"
						}
					}
					break
				}
			}
			a.idiom(label, node)
		}
	case "enum_specifier":
		if node.ChildByFieldName("body") != nil {
			a.idiom("enum definitions", node)
		}
	case "sizeof_expression":
		if sizeofVariable(node, a.content) {
			a.idiom("sizeof variable expressions", node)
		}
	case "function_declarator":
		if functionDeclarator(node) != nil {
			parameters := node.ChildByFieldName("parameters")
			if parameters != nil && parameters.NamedChildCount() == 1 {
				parameter := parameters.NamedChild(0)
				if parameter.Type() == "parameter_declaration" && parameter.ChildByFieldName("declarator") == nil {
					if kind := parameter.ChildByFieldName("type"); kind != nil && kind.Content(a.content) == "void" {
						a.idiom("explicit void parameter lists", parameters)
					}
				}
			}
		}
	case "declaration":
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.FieldNameForChild(i) == "declarator" {
				if prototype := functionDeclarator(node.Child(i)); prototype != nil {
					a.idiom("function forward declarations", prototype)
				}
			}
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			if node.FieldNameForChild(i) == "declarator" && node.Child(i).Type() != "init_declarator" {
				a.declaredName(node.Child(i))
			}
		}
	case "pointer_declarator":
		a.pointerStar(node)
	case "if_statement", "while_statement", "for_statement", "switch_statement":
		a.counts.stats.Conditions++
		if node.Type() == "while_statement" {
			body := node.ChildByFieldName("body")
			condition := node.ChildByFieldName("condition")
			if body != nil && body.Type() == "expression_statement" && body.NamedChildCount() == 0 && condition != nil &&
				containsWaitCall(condition, a.content) {
				a.idiom("empty wait-result loops", node)
			}
		}
		if kw := node.Child(0); kw != nil && kw.EndByte() < uint32(len(a.content)) {
			a.counts.choose("keyword_paren", map[bool]string{true: "space", false: "tight"}[isSpace(a.content[kw.EndByte()])])
			a.exactGap("keyword_gap", kw.EndByte(), node.Child(1).StartByte())
		}
	case "compound_statement":
		a.block(node)
	case "else_clause":
		a.elsePlacement(node)
	case "cast_expression":
		if t := node.ChildByFieldName("type"); t != nil && strings.TrimSpace(t.Content(a.content)) == "void" {
			value := node.ChildByFieldName("value")
			for value != nil && value.Type() == "parenthesized_expression" {
				value = value.NamedChild(0)
			}
			if value != nil && value.Type() == "identifier" {
				a.idiom("unused-variable (void) casts", node)
			}
			if value != nil && value.Type() == "call_expression" {
				a.idiom("discarded call results (void)", node)
			}
		}
	case "attribute_specifier", "attribute_declaration":
		if hasUnusedAttribute(node, a.content) {
			a.idiom("unused attributes", node)
		}
	case "identifier":
		switch node.Content(a.content) {
		case "EXIT_FAILURE", "EXIT_SUCCESS":
			a.idiom("EXIT_SUCCESS/EXIT_FAILURE", node)
		case "EINTR", "EAGAIN":
			a.idiom("errno retries", node)
		case "errno":
			a.idiom("errno access", node)
		case "__func__":
			a.idiom("__func__", node)
		case "STDOUT_FILENO", "STDERR_FILENO", "STDIN_FILENO":
			a.idiom("STDOUT_FILENO-style descriptors", node)
		}
	case "primitive_type", "type_identifier":
		switch node.Content(a.content) {
		case "size_t", "ssize_t":
			a.idiom("size_t/ssize_t", node)
		case "bool", "_Bool":
			a.idiom("boolean types", node)
		case "sem_t":
			a.idiom("POSIX semaphores", node)
		}
	case ",":
		if p := node.Parent(); p != nil && (p.Type() == "argument_list" || p.Type() == "parameter_list") && node.EndByte() < uint32(len(a.content)) {
			a.counts.choose("comma_space", map[bool]string{true: "space", false: "tight"}[isSpace(a.content[node.EndByte()])])
			if next := node.NextSibling(); next != nil {
				a.exactGap("comma_gap", node.EndByte(), next.StartByte())
			}
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		a.walk(node.Child(i))
	}
}

func hasChildType(node *sitter.Node, kind string) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		if node.Child(i).Type() == kind {
			return true
		}
	}
	return false
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func (a *styleAnalysis) function(node *sitter.Node) {
	a.counts.functions++
	if prev := node.PrevSibling(); prev != nil && prev.Type() == "comment" && prev.StartByte() >= a.header &&
		node.StartPoint().Row-prev.EndPoint().Row <= 1 {
		a.counts.documentedFunctions++
		a.evidence("doc_headers", "Function documentation", int(prev.StartPoint().Row)+1, int(node.StartPoint().Row)+1, prev.Content(a.content))
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "storage_class_specifier" && child.Content(a.content) == "static" {
			a.idiom("static functions", child)
		}
	}
	body := node.ChildByFieldName("body")
	declarator := node.ChildByFieldName("declarator")
	if body != nil && startsWithGuard(body, a.content) {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			stmt := body.NamedChild(i)
			if stmt.Type() != "comment" && stmt.Type() != "declaration" {
				a.idiom("guard clauses", stmt)
				break
			}
		}
	}
	if body != nil && declarator != nil {
		a.counts.choose("brace_function", map[bool]string{true: "same_line", false: "next_line"}[body.StartPoint().Row == declarator.EndPoint().Row])
	}
	if declarator != nil {
		if name := nameOf(declarator, a.content); name != "" {
			a.namingStyle(name)
		}
	}
}

func (a *styleAnalysis) call(node *sitter.Node) {
	fn := node.ChildByFieldName("function")
	args := node.ChildByFieldName("arguments")
	if fn == nil || fn.Type() != "identifier" {
		return
	}
	if args != nil {
		a.counts.choose("call_paren", map[bool]string{true: "tight", false: "space"}[fn.EndByte() == args.StartByte()])
		a.exactGap("call_gap", fn.EndByte(), args.StartByte())
	}
	name := fn.Content(a.content)
	if posixSemaphoreCalls[name] {
		a.idiom("POSIX semaphores", node)
	}
	if name == "exit" || name == "_exit" {
		for owner := node.Parent(); owner != nil; owner = owner.Parent() {
			if owner.Type() == "function_definition" {
				if a.signalHandlers[nameOf(owner.ChildByFieldName("declarator"), a.content)] {
					a.idiom("exit from registered signal handlers", node)
				}
				break
			}
		}
	}
	switch name {
	case "perror", "snprintf", "assert", "strerror", "memset":
		a.idiom(name, node)
	}
	if !checkableCalls[name] || insideMacro(node) {
		return
	}
	switch checkState(node, a.content) {
	case resultChecked:
		a.counts.checkableCalls++
		a.counts.checkedCalls++
		a.nodeEvidence("error_checks", name+" result checked", node)
		if name == "wait" || name == "waitpid" {
			a.idiom("checked wait/waitpid results", node)
		}
	case resultIgnored:
		a.counts.checkableCalls++
	}
}

// Count initialized buffers as a measured defensive idiom.
func zeroInitializedBuffer(node *sitter.Node, content []byte) bool {
	declarator, value := node.ChildByFieldName("declarator"), node.ChildByFieldName("value")
	if declarator == nil || value == nil {
		return false
	}
	for declarator.Type() == "pointer_declarator" {
		if declarator = declarator.ChildByFieldName("declarator"); declarator == nil {
			return false
		}
	}
	if declarator.Type() != "array_declarator" {
		return false
	}
	if value.Type() == "initializer_list" && value.NamedChildCount() == 1 {
		value = value.NamedChild(0)
	}
	switch strings.TrimSpace(value.Content(content)) {
	case "0", `""`, `'\0'`, "{0}", "NULL":
		return true
	}
	return false
}

// Arguments wrapped onto new lines and lined up under the first one, as formatters and assistants do.
func alignedWrappedArguments(node *sitter.Node) bool {
	if node.Parent() == nil || node.Parent().Type() != "call_expression" || node.StartPoint().Row == node.EndPoint().Row {
		return false
	}
	first := node.NamedChild(0)
	if first == nil || first.StartPoint().Row != node.StartPoint().Row {
		return false
	}
	col := first.StartPoint().Column
	lastRow := first.StartPoint().Row
	wrapped := 0
	for i := 1; i < int(node.NamedChildCount()); i++ {
		arg := node.NamedChild(i)
		if arg.StartPoint().Row == lastRow {
			continue
		}
		if arg.StartPoint().Column != col {
			return false
		}
		lastRow = arg.StartPoint().Row
		wrapped++
	}
	return wrapped > 0
}

// A function that opens by bailing out on bad input: `if (!p) return -1;`.
func startsWithGuard(body *sitter.Node, content []byte) bool {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		stmt := body.NamedChild(i)
		switch stmt.Type() {
		case "comment", "declaration":
			continue
		case "if_statement":
			if stmt.ChildByFieldName("alternative") != nil {
				return false
			}
			return exitsEarly(stmt.ChildByFieldName("consequence"), content)
		}
		return false
	}
	return false
}

func exitsEarly(node *sitter.Node, content []byte) bool {
	if node == nil {
		return false
	}
	switch node.Type() {
	case "compound_statement":
		n := int(node.NamedChildCount())
		return n > 0 && exitsEarly(node.NamedChild(n-1), content)
	case "return_statement", "goto_statement", "break_statement", "continue_statement":
		return true
	case "expression_statement":
		if call := node.NamedChild(0); call != nil && call.Type() == "call_expression" {
			if fn := call.ChildByFieldName("function"); fn != nil {
				switch fn.Content(content) {
				case "exit", "_exit", "abort":
					return true
				}
			}
		}
	}
	return false
}

func insideMacro(node *sitter.Node) bool {
	for p := node.Parent(); p != nil; p = p.Parent() {
		if p.Type() == "preproc_function_def" || p.Type() == "preproc_def" {
			return true
		}
	}
	return false
}

type resultUse int

const (
	resultIgnored resultUse = iota
	resultChecked
	// Passed straight into another call, so whether it is checked depends on code we can't see.
	resultUnknown
)

func fieldOf(parent, child *sitter.Node) string {
	for i := 0; i < int(parent.ChildCount()); i++ {
		if c := parent.Child(i); c.StartByte() == child.StartByte() && c.EndByte() == child.EndByte() && c.Type() == child.Type() {
			return parent.FieldNameForChild(i)
		}
	}
	return ""
}

var comparisonOperators = map[string]bool{"<": true, ">": true, "<=": true, ">=": true, "==": true, "!=": true}

// checkState follows a value up the tree until it is tested, stored, or dropped.
func checkState(node *sitter.Node, content []byte) resultUse {
	n := node
	for p := n.Parent(); p != nil; n, p = p, p.Parent() {
		switch p.Type() {
		case "parenthesized_expression", "comma_expression":
			continue
		case "binary_expression":
			if op := p.ChildByFieldName("operator"); op != nil && comparisonOperators[op.Type()] {
				return resultChecked
			}
			continue
		case "unary_expression":
			if op := p.ChildByFieldName("operator"); op != nil && op.Type() == "!" {
				return resultChecked
			}
			continue
		case "cast_expression":
			if t := p.ChildByFieldName("type"); t != nil && strings.TrimSpace(t.Content(content)) == "void" {
				return resultIgnored
			}
			continue
		case "if_statement", "while_statement", "do_statement", "switch_statement", "for_statement", "conditional_expression":
			if fieldOf(p, n) == "condition" {
				return resultChecked
			}
			return resultIgnored
		case "return_statement":
			return resultUnknown
		case "argument_list":
			return resultUnknown
		case "assignment_expression":
			if fieldOf(p, n) != "right" {
				return resultIgnored
			}
			return storedValueState(p.ChildByFieldName("left"), p, content)
		case "init_declarator":
			return storedValueState(p.ChildByFieldName("declarator"), p, content)
		default:
			return resultIgnored
		}
	}
	return resultIgnored
}

// A stored result counts as checked when the same function later tests that variable.
func storedValueState(target, after *sitter.Node, content []byte) resultUse {
	name := nameOf(target, content)
	if name == "" {
		return resultIgnored
	}
	body := after
	for body != nil && body.Type() != "compound_statement" {
		body = body.Parent()
	}
	if body == nil {
		return resultIgnored
	}
	found := false
	invalidated := false
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if found || invalidated || n.EndByte() <= after.EndByte() {
			return
		}
		if n.StartByte() >= after.EndByte() {
			switch n.Type() {
			case "assignment_expression":
				if nameOf(n.ChildByFieldName("left"), content) == name {
					invalidated = true
					return
				}
			case "init_declarator", "declaration":
				for i := 0; i < int(n.ChildCount()); i++ {
					if n.FieldNameForChild(i) == "declarator" && nameOf(n.Child(i), content) == name {
						invalidated = true
						return
					}
				}
			}
		}
		if n.Type() == "identifier" && n.StartByte() >= after.EndByte() && n.Content(content) == name {
			if directlyTested(n) {
				found = true
				return
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			visit(n.Child(i))
		}
	}
	visit(body)
	if found {
		return resultChecked
	}
	return resultIgnored
}

func directlyTested(node *sitter.Node) bool {
	for p := node.Parent(); p != nil; node, p = p, p.Parent() {
		switch p.Type() {
		case "parenthesized_expression":
			continue
		case "binary_expression":
			if op := p.ChildByFieldName("operator"); op != nil && comparisonOperators[op.Type()] {
				return true
			}
		case "unary_expression":
			if op := p.ChildByFieldName("operator"); op != nil && op.Type() == "!" {
				return true
			}
		case "if_statement", "while_statement", "do_statement", "for_statement", "conditional_expression":
			return fieldOf(p, node) == "condition"
		default:
			return false
		}
	}
	return false
}

func (a *styleAnalysis) binaryIdioms(node *sitter.Node) {
	left, right := node.ChildByFieldName("left"), node.ChildByFieldName("right")
	op := node.ChildByFieldName("operator")
	if left == nil || right == nil || op == nil {
		return
	}
	switch {
	case op.Type() == "/" && left.Type() == "sizeof_expression" && right.Type() == "sizeof_expression":
		a.idiom("sizeof array length", node)
	case (op.Type() == "==" || op.Type() == "!=") && (left.Type() == "null" || right.Type() == "null"):
		a.idiom("NULL comparisons", node)
	}
}

func (a *styleAnalysis) operatorSpacing(op *sitter.Node) {
	if op == nil || op.StartByte() == 0 || int(op.EndByte()) >= len(a.content) {
		return
	}
	before, after := isSpace(a.content[op.StartByte()-1]), isSpace(a.content[op.EndByte()])
	if left, right := op.PrevSibling(), op.NextSibling(); left != nil && right != nil {
		family := "arithmetic_gap"
		switch op.Type() {
		case "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
			family = "assignment_gap"
		case "==", "!=", "<", "<=", ">", ">=":
			family = "comparison_gap"
		case "&&", "||":
			family = "logical_gap"
		}
		a.counts.choose(family, gapLabel(a.content[left.EndByte():op.StartByte()])+" / "+gapLabel(a.content[op.EndByte():right.StartByte()]))
	}
	switch {
	case before && after:
		a.counts.choose("operator_space", "both")
	case !before && !after:
		a.counts.choose("operator_space", "none")
	default:
		a.counts.choose("operator_space", "one_side")
	}
}

func gapLabel(gap []byte) string {
	text := string(gap)
	if strings.TrimSpace(text) != "" {
		return "comment"
	}
	if strings.ContainsAny(text, "\r\n") {
		return "newline"
	}
	if strings.Contains(text, "\t") {
		return fmt.Sprintf("%d tabs + %d spaces", strings.Count(text, "\t"), strings.Count(text, " "))
	}
	return fmt.Sprintf("%d spaces", len(text))
}

func (a *styleAnalysis) exactGap(key string, start, end uint32) {
	if start <= end && end <= uint32(len(a.content)) {
		a.counts.choose(key, gapLabel(a.content[start:end]))
	}
}

func (a *styleAnalysis) pointerStar(node *sitter.Node) {
	star := node.Child(0)
	if star == nil || star.Type() != "*" || star.StartByte() == 0 || int(star.EndByte()) >= len(a.content) {
		return
	}
	before, after := isSpace(a.content[star.StartByte()-1]), isSpace(a.content[star.EndByte()])
	if before == after {
		// `int * p` and `char**argv` are rare either way; record them so they count as inconsistency.
		a.counts.choose("pointer_star", "other")
		return
	}
	a.counts.choose("pointer_star", map[bool]string{true: "with_name", false: "with_type"}[before])
}

// Record variation between sibling statements at the same nesting level.
func (a *styleAnalysis) block(node *sitter.Node) {
	if p := node.Parent(); p != nil {
		switch p.Type() {
		case "if_statement", "else_clause", "for_statement", "while_statement", "do_statement", "switch_statement":
			if prev := node.PrevSibling(); prev != nil {
				a.counts.choose("brace_control", map[bool]string{true: "same_line", false: "next_line"}[node.StartPoint().Row == prev.EndPoint().Row])
			}
		}
	}
	firstCol := -1
	lastRow := -1
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		row := int(child.StartPoint().Row)
		if child.Type() == "comment" || row == lastRow || !startsLine(a.content, child.StartByte()) {
			continue
		}
		lastRow = int(child.EndPoint().Row)
		col := int(child.StartPoint().Column)
		if firstCol < 0 {
			firstCol = col
			continue
		}
		a.counts.choose("indent_alignment", map[bool]string{true: "aligned", false: "drifted"}[col == firstCol])
	}
}

func startsLine(content []byte, offset uint32) bool {
	for i := int(offset) - 1; i >= 0; i-- {
		switch content[i] {
		case '\n':
			return true
		case ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

func (a *styleAnalysis) elsePlacement(node *sitter.Node) {
	prev := node.PrevSibling()
	if prev == nil || prev.Type() != "compound_statement" {
		return
	}
	a.counts.choose("else_placement", map[bool]string{true: "cuddled", false: "own_line"}[node.StartPoint().Row == prev.EndPoint().Row])
}

func nameOf(node *sitter.Node, content []byte) string {
	for node != nil {
		if node.Type() == "identifier" || node.Type() == "field_identifier" || node.Type() == "type_identifier" {
			return node.Content(content)
		}
		node = node.ChildByFieldName("declarator")
	}
	return ""
}

func (a *styleAnalysis) declaredName(node *sitter.Node) {
	target := node
	if node.Type() == "init_declarator" || node.Type() == "parameter_declaration" {
		target = node.ChildByFieldName("declarator")
	}
	name := nameOf(target, a.content)
	if name == "" {
		return
	}
	if functionDeclarator(target) != nil {
		return
	}
	upper, lower := 0, false
	for _, letter := range name {
		if unicode.IsUpper(letter) {
			upper++
		}
		lower = lower || unicode.IsLower(letter)
	}
	if upper >= 2 && !lower {
		a.idiom("uppercase variable names", target)
	} else if first, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(first) {
		a.idiom("capitalized variable or field names", target)
	}
	a.namingStyle(name)
}

func (a *styleAnalysis) namingStyle(name string) {
	hasUnderscore := strings.Contains(strings.Trim(name, "_"), "_")
	hasUpper, hasLower := false, false
	for _, r := range name {
		hasUpper = hasUpper || unicode.IsUpper(r)
		hasLower = hasLower || unicode.IsLower(r)
	}
	switch {
	case !hasLower:
		return
	case hasUnderscore && hasUpper:
		a.counts.namingStyles["mixed"]++
	case hasUnderscore:
		a.counts.namingStyles["snake_case"]++
	case hasUpper:
		a.counts.namingStyles["camelCase"]++
	}
}

var (
	phaseComment     = regexp.MustCompile(`(?i)^(?:(?:fase|phase|paso|step|etapa)\s+\d+\s*[:.)-]|\d+[.)]\s+)`)
	codeLikeComment  = regexp.MustCompile(`(;\s*$)|(^\s*[{}]\s*$)|(^\s*#\s*include)|(^\s*(if|for|while|return|else)\b.*[(;{])|(\w\s*\(.*\)\s*;?\s*$)`)
	doxygenTag       = regexp.MustCompile(`[@\\](brief|param(\[\w+\])?\s+\w+|returns?|retval|note|details?|pre|post)\b`)
	commentDecorated = regexp.MustCompile(`^[\s=*#~_\-/+]*$`)
)

type styleCommentUnit struct {
	text               string
	startLine, endLine int
}

// Wrapped line comments form one measurement and one source range.
func (a *styleAnalysis) commentUnits() []styleCommentUnit {
	var units []styleCommentUnit
	lastLineCommentRow := -2
	for _, node := range a.commentList {
		text := node.Content(a.content)
		if node.StartByte() < a.header {
			continue
		}
		if strings.HasPrefix(text, "//") {
			body := strings.TrimLeft(text, "/!")
			if body != "" {
				a.counts.choose("comment_space", map[bool]string{true: "space", false: "tight"}[body[0] == ' ' || body[0] == '\t'])
			}
			row := int(node.StartPoint().Row)
			standalone := startsLine(a.content, node.StartByte())
			if standalone && row == lastLineCommentRow+1 && len(units) > 0 {
				units[len(units)-1].text += " " + strings.TrimSpace(body)
				units[len(units)-1].endLine = row + 1
				lastLineCommentRow = row
				continue
			}
			if standalone {
				lastLineCommentRow = row
			} else {
				lastLineCommentRow = -2
			}
			units = append(units, styleCommentUnit{strings.TrimSpace(body), row + 1, row + 1})
			continue
		}
		lastLineCommentRow = -2
		body := strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
		lines := strings.Split(body, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimLeft(strings.TrimSpace(line), "*! ")
		}
		units = append(units, styleCommentUnit{strings.TrimSpace(strings.Join(lines, " ")), int(node.StartPoint().Row) + 1, int(node.EndPoint().Row) + 1})
	}
	return units
}

func (a *styleAnalysis) comments() {
	for _, unit := range a.commentUnits() {
		if commentDecorated.MatchString(unit.text) || codeLikeComment.MatchString(unit.text) {
			continue
		}
		letters := 0
		for _, r := range unit.text {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if letters < 3 {
			continue
		}
		a.counts.eligibleComments++
		if phaseComment.MatchString(unit.text) {
			a.counts.idioms["numbered solution-phase comments"]++
			a.evidence("defensive_idioms", "Numbered solution-phase comment", unit.startLine, unit.endLine, unit.text)
		}
		if isProse(unit.text) {
			a.counts.proseComments++
			a.evidence("prose_comments", "Sentence-style comment", unit.startLine, unit.endLine, unit.text)
		}
	}
}

// Count capitalized prose independently of comment language.
func isProse(text string) bool {
	text = strings.TrimSpace(doxygenTag.ReplaceAllString(text, ""))
	text = strings.TrimLeft(text, ":-– ")
	if len(strings.Fields(text)) < 4 {
		return false
	}
	first, _ := utf8.DecodeRuneInString(text)
	if !unicode.IsUpper(first) {
		return false
	}
	end := strings.TrimRight(text, " )\"'")
	return strings.HasSuffix(end, ".") && !strings.HasSuffix(end, "...")
}

// Normalized Shannon entropy of one formatting choice; 0 means the same choice every time.
func choiceEntropy(counts choiceCounts, variants int) (float64, int) {
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 || variants < 2 {
		return 0, total
	}
	h := 0.0
	for _, n := range counts {
		if n == 0 {
			continue
		}
		p := float64(n) / float64(total)
		h -= p * math.Log(p)
	}
	return h / math.Log(float64(variants)), total
}

var choiceVariants = map[string]int{
	"blank_lines": 2, "trailing_space": 2, "indent_char": 3, "indent_alignment": 2,
	"keyword_paren": 2, "operator_space": 3, "comma_space": 2, "call_paren": 2,
	"brace_function": 2, "brace_control": 2, "else_placement": 2, "pointer_star": 3, "comment_space": 2,
}

var choiceLabels = map[string]string{
	"blank_lines": "blank lines", "trailing_space": "trailing spaces", "indent_char": "tabs vs spaces",
	"indent_alignment": "indentation", "keyword_paren": "space after if/for/while", "operator_space": "spaces around operators",
	"comma_space": "space after commas", "call_paren": "space before call parentheses", "brace_function": "function brace placement",
	"brace_control": "brace placement", "else_placement": "else placement", "pointer_star": "pointer star placement",
	"comment_space":  "space after //",
	"assignment_gap": "assignment spacing", "comparison_gap": "comparison spacing", "logical_gap": "logical operator spacing",
	"arithmetic_gap": "arithmetic spacing", "keyword_gap": "control parentheses spacing", "comma_gap": "comma spacing", "call_gap": "call parentheses spacing",
}

func sampleSupport(samples, target int) float64 {
	return math.Sqrt(math.Min(1, float64(samples)/float64(target)))
}

func scoreStyle(counts *styleCounts) *domain.AIStyleReport {
	var features []domain.AIStyleFeature
	add := func(def styleFeatureDef, value float64, detail string) {
		samples, target := 0, 1
		switch def.key {
		case "prose_comments":
			samples, target = counts.eligibleComments, 10
		case "doc_headers":
			samples, target = counts.functions, 8
		case "error_checks":
			samples, target = counts.checkableCalls, 10
		case "defensive_idioms":
			samples, target = counts.stats.TokenCount, 400
		}
		reliability := 1.0
		if def.evidence == evidenceReview {
			reliability = sampleSupport(samples, target)
		}
		if reliability > 0 && reliability < 1 {
			detail += fmt.Sprintf("; sample support %.0f%%", reliability*100)
		}
		features = append(features, domain.AIStyleFeature{
			Key: def.key, Label: def.label, Evidence: def.evidence, Value: round3(value),
			Score: round3(def.ramp.score(value)), Weight: def.weight, Detail: detail,
			SampleCount: samples, Reliability: round3(reliability),
			Locations: counts.locations[def.key], LocationCount: counts.locationCounts[def.key],
		})
	}

	// Keep absent comments in the denominator when enough code is present.
	if counts.codeLines >= minCodeLines || counts.stats.TokenCount >= minMetricTokens || counts.eligibleComments >= minEligibleComments {
		detail := fmt.Sprintf("%d of %d comments are full sentences", counts.proseComments, counts.eligibleComments)
		if counts.eligibleComments == 0 {
			detail = "No comments besides the file header"
		}
		add(featureProseComments, ratio(counts.proseComments, max(counts.eligibleComments, minEligibleComments)), detail)
	}
	if counts.functions >= minFunctions {
		add(featureDocHeaders, ratio(counts.documentedFunctions, counts.functions),
			fmt.Sprintf("%d of %d functions have a comment right above them", counts.documentedFunctions, counts.functions))
	}
	if counts.checkableCalls >= minCheckableCalls {
		add(featureErrorChecks, ratio(counts.checkedCalls, counts.checkableCalls),
			fmt.Sprintf("%d of %d write/read/pipe/malloc-style calls check the result", counts.checkedCalls, counts.checkableCalls))
	}
	if counts.codeLines >= minCodeLines || counts.stats.TokenCount >= minMetricTokens {
		total := 0
		for _, n := range counts.idioms {
			total += min(n, maxIdiomCount)
		}
		effectiveLines := max(counts.codeLines, (counts.stats.TokenCount+11)/12)
		add(featureIdioms, float64(total)*100/float64(effectiveLines), idiomDetail(counts.idioms, counts.codeLines))
	}
	if h, detail, ok := formattingEntropy(counts.choices); ok {
		add(featureFormatEntropy, h, detail)
	}
	if h, n := choiceEntropy(counts.namingStyles, 3); n >= minNamedStyles {
		add(featureNamingEntropy, h, namingDetail(counts.namingStyles))
	}

	totalWeight := 0.0
	for _, def := range reviewFeatures {
		totalWeight += def.weight
	}
	usedWeight, weighted := 0.0, 0.0
	commentEvidence, callEvidence, idiomEvidence := false, false, false
	for _, f := range features {
		if f.Evidence == evidenceContext {
			continue
		}
		usedWeight += f.Weight
		weighted += f.Weight * f.Score * f.Reliability
		if f.Score*f.Reliability >= 0.5 {
			switch f.Key {
			case "prose_comments", "doc_headers":
				commentEvidence = true
			case "error_checks":
				callEvidence = true
			case "defensive_idioms":
				idiomEvidence = true
			}
		}
	}

	counts.stats.CodeLines = counts.codeLines
	report := &domain.AIStyleReport{Features: features, SourceStats: &counts.stats, Formatting: formattingChoices(counts.choices)}
	if usedWeight > 0 {
		report.Score = round3(weighted / totalWeight)
	}
	supported := 0
	for _, evidence := range []bool{commentEvidence, callEvidence, idiomEvidence} {
		if evidence {
			supported++
		}
	}
	report.Flagged = report.Score >= styleFlagScore && usedWeight/totalWeight >= styleMinWeightUsed && supported >= 2
	return report
}

func formattingEntropy(choices map[string]choiceCounts) (float64, string, bool) {
	type point struct {
		key string
		h   float64
	}
	var points []point
	for key, counts := range choices {
		if key == "trailing_space" || key == "blank_lines" || key == "keyword_paren" || key == "operator_space" || key == "comma_space" || key == "call_paren" {
			continue
		}
		variants := choiceVariants[key]
		if variants == 0 {
			variants = max(2, len(counts))
		}
		h, n := choiceEntropy(counts, variants)
		if n >= minChoiceSamples {
			points = append(points, point{key, h})
		}
	}
	if len(points) < 4 {
		return 0, "", false
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].h != points[j].h {
			return points[i].h > points[j].h
		}
		return points[i].key < points[j].key
	})
	sum := 0.0
	for _, p := range points {
		sum += p.h
	}
	mean := sum / float64(len(points))
	var varied []string
	for _, p := range points {
		if p.h >= 0.1 && len(varied) < 3 {
			varied = append(varied, choiceLabels[p.key])
		}
	}
	detail := fmt.Sprintf("Consistent across all %d formatting choices measured", len(points))
	if len(varied) > 0 {
		detail = fmt.Sprintf("Varies in %s (%d choices measured)", strings.Join(varied, ", "), len(points))
	}
	return mean, detail, true
}

func formattingChoices(choices map[string]choiceCounts) []domain.AIFormattingChoice {
	var result []domain.AIFormattingChoice
	for key, counts := range choices {
		if !strings.HasSuffix(key, "_gap") && key != "indent_alignment" && key != "indent_char" {
			continue
		}
		variants := max(2, len(counts))
		h, n := choiceEntropy(counts, variants)
		if n < minChoiceSamples {
			continue
		}
		choice := domain.AIFormattingChoice{Key: key, Label: choiceLabels[key], SampleCount: n, Entropy: round3(h)}
		for variant, count := range counts {
			if count > choice.DominantCount || (count == choice.DominantCount && variant < choice.Dominant) {
				choice.Dominant, choice.DominantCount = variant, count
			}
		}
		result = append(result, choice)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func idiomDetail(idioms map[string]int, lines int) string {
	if len(idioms) == 0 {
		return fmt.Sprintf("None in %d lines of code", lines)
	}
	keys := make([]string, 0, len(idioms))
	for k := range idioms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if idioms[keys[i]] != idioms[keys[j]] {
			return idioms[keys[i]] > idioms[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s ×%d", k, idioms[k]))
	}
	return strings.Join(parts, ", ")
}

func namingDetail(styles choiceCounts) string {
	keys := []string{"snake_case", "camelCase", "mixed"}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if styles[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, styles[k]))
		}
	}
	return strings.Join(parts, ", ")
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
