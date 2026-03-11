package domain

type AIDictionaryEntryError struct {
	EntryID string
	Err     string
}

type AIDictionaryMatch struct {
	EntryID  string
	Category string
	Title    string
	Jaccard  float64
	Flagged  bool
	Spans    []MatchSpan
}

type AISubmissionResult struct {
	SubmissionIndex int
	SubmissionID    string
	SourceFile      string
	FunctionCount   int
	MatchCount      int
	BestScore       float64
	Flagged         bool
	ParseError      string
	Matches         []AIDictionaryMatch
}

type AIDetectionReport struct {
	SourceFile           string
	DictionaryEntryCount int
	DictionaryUsable     int
	DictionaryErrors     []AIDictionaryEntryError
	Results              []AISubmissionResult
}
