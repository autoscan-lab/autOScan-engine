package domain

type AIDictionaryEntryError struct {
	EntryID string `json:"entry_id"`
	Err     string `json:"error"`
}

type AIDictionaryMatch struct {
	EntryID  string      `json:"entry_id"`
	Category string      `json:"category"`
	Title    string      `json:"title"`
	Jaccard  float64     `json:"jaccard"`
	Flagged  bool        `json:"flagged"`
	Spans    []MatchSpan `json:"spans,omitempty"`
}

type AISubmissionResult struct {
	SubmissionID  string              `json:"id"`
	SourceFile    string              `json:"source_file,omitempty"`
	FunctionCount int                 `json:"function_count"`
	MatchCount    int                 `json:"match_count,omitempty"`
	BestScore     float64             `json:"best_score"`
	Flagged       bool                `json:"flagged"`
	ParseError    string              `json:"parse_error,omitempty"`
	Matches       []AIDictionaryMatch `json:"matches,omitempty"`
}

type AIDetectionReport struct {
	SourceFile           string                   `json:"source_file"`
	DictionaryEntryCount int                      `json:"dictionary_entry_count"`
	DictionaryUsable     int                      `json:"dictionary_usable"`
	DictionaryErrors     []AIDictionaryEntryError `json:"dictionary_errors,omitempty"`
	Submissions          []AISubmissionResult     `json:"submissions"`
}
