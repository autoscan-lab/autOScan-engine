package domain

type CompareConfig struct {
	MinMatchTokens int
	MinFuncTokens  int
	ScoreThreshold float64
}

// Tokens and Spans are index-aligned; a zero token separates functions and never matches.
type FileFingerprint struct {
	Tokens        []uint64
	Spans         []Span
	TokenCount    int
	FunctionCount int
	Content       []byte
	LineOffsets   []int
}

type Span struct {
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

type TileMatch struct {
	Hash   string      `json:"hash"`
	SpansA []MatchSpan `json:"spans_a,omitempty"`
	SpansB []MatchSpan `json:"spans_b,omitempty"`
}

type PlagiarismResult struct {
	FileA             string      `json:"file_a"`
	FileB             string      `json:"file_b"`
	SimilarityPercent float64     `json:"similarity_percent"`
	Flagged           bool        `json:"flagged"`
	Matches           []TileMatch `json:"matches,omitempty"`
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
