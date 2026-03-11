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
	StartLine int // 1-based line number
	StartCol  int // 1-based column number
	EndLine   int // 1-based line number
	EndCol    int // 1-based column number
	Snippet   string
}

type WindowMatch struct {
	Hash   string
	SpansA []MatchSpan
	SpansB []MatchSpan
}

type PlagiarismResult struct {
	FileA             string
	FileB             string
	FunctionCountA    int
	FunctionCountB    int
	ExactMatches      int
	WindowMatches     int
	WindowUnion       int
	WindowJaccard     float64
	PerFuncSimilarity float64
	Flagged           bool
	Matches           []WindowMatch
}

type SimilarityPairResult struct {
	AIndex int
	BIndex int
	Result PlagiarismResult
}
