package domain

type CompareConfig struct {
	WindowSize     int
	MinFuncTokens  int
	ScoreThreshold float64
}

type FileFingerprint struct {
	FunctionHashes  map[string]struct{}
	WindowHashes    map[string]struct{}
	WindowSpans     map[string][]Span
	FunctionCount   int
	FunctionWindows []map[string]struct{}
	Content         []byte
	LineOffsets     []int
}

type Span struct {
	Start uint32 // Start byte offset
	End   uint32 // End byte offset
}

type TokenSpan struct {
	Token string
	Start uint32 // Start byte offset
	End   uint32 // End byte offset
}

type MatchSpan struct {
	StartLine int    `json:"start_line"` // 1-based line number
	StartCol  int    `json:"start_col"`  // 1-based column number
	EndLine   int    `json:"end_line"`   // 1-based line number
	EndCol    int    `json:"end_col"`    // 1-based column number
	Snippet   string `json:"snippet"`
}

type WindowMatch struct {
	Hash   string      `json:"hash"`
	SpansA []MatchSpan `json:"spans_a,omitempty"`
	SpansB []MatchSpan `json:"spans_b,omitempty"`
}

type PlagiarismResult struct {
	FileA             string        `json:"file_a"`
	FileB             string        `json:"file_b"`
	FunctionCountA    int           `json:"function_count_a"`
	FunctionCountB    int           `json:"function_count_b"`
	ExactMatches      int           `json:"exact_matches"`
	WindowMatches     int           `json:"window_matches"`
	WindowUnion       int           `json:"window_union,omitempty"`
	WindowJaccard     float64       `json:"window_jaccard"`
	PerFuncSimilarity float64       `json:"per_func_similarity"`
	Flagged           bool          `json:"flagged"`
	Matches           []WindowMatch `json:"matches,omitempty"`
}

type SimilarityReport struct {
	SourceFile string                 `json:"source_file"`
	Pairs      []SimilarityPairResult `json:"pairs"`
}

type SimilarityPairResult struct {
	A string `json:"a"`
	B string `json:"b"`
	PlagiarismResult
}
