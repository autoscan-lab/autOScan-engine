package domain

type AIDictionaryEntryError struct {
	EntryID string `json:"entry_id"`
	Err     string `json:"error"`
}

type AIDictionaryMatch struct {
	EntryID string `json:"entry_id"`
	Title   string `json:"title"`
	// Share of the pattern found in the submission.
	Score float64     `json:"score"`
	Spans []MatchSpan `json:"spans,omitempty"`
}

// AITell records review evidence; typography is context only.
type AITell struct {
	Flagged bool   `json:"flagged"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"end_line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

type AIStyleFeature struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Review features raise the score; context features do not affect it.
	Evidence string  `json:"evidence"`
	Value    float64 `json:"value"`
	// Strength of the evidence, from 0 (none) to 1 (as strong as it gets).
	Score         float64            `json:"score"`
	Weight        float64            `json:"weight"`
	Detail        string             `json:"detail"`
	Locations     []AISourceEvidence `json:"locations,omitempty"`
	LocationCount int                `json:"location_count"`
}

type AISourceEvidence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Label     string `json:"label"`
	Snippet   string `json:"snippet,omitempty"`
}

type AIScoreContribution struct {
	Key        string             `json:"key"`
	FeatureKey string             `json:"feature_key,omitempty"`
	Label      string             `json:"label"`
	Points     float64            `json:"points"`
	Detail     string             `json:"detail"`
	Locations  []AISourceEvidence `json:"locations,omitempty"`
}

type AIScoreReport struct {
	// A versioned heuristic from 0 to 100, never an authorship probability.
	Score         float64               `json:"score"`
	Method        string                `json:"method"`
	Contributions []AIScoreContribution `json:"contributions"`
}

type AIStyleReport struct {
	// Uncalibrated review score, not an authorship probability.
	Score float64 `json:"score"`
	// Share of the run's submissions with a lower score.
	Percentile float64          `json:"percentile"`
	CohortSize int              `json:"cohort_size"`
	Flagged    bool             `json:"flagged"`
	Features   []AIStyleFeature `json:"features"`
}

type AITokenMetrics struct {
	TokenCount          int      `json:"token_count"`
	EntropyBits         float64  `json:"entropy_bits"`
	CohortSize          int      `json:"cohort_size"`
	CrossEntropyBits    *float64 `json:"cross_entropy_bits,omitempty"`
	Perplexity          *float64 `json:"perplexity,omitempty"`
	SurprisalStddevBits *float64 `json:"surprisal_stddev_bits,omitempty"`
}

// AISimilarLink points at a submission flagged on its own evidence that this one closely resembles.
type AISimilarLink struct {
	SubmissionID      string  `json:"id"`
	SimilarityPercent float64 `json:"similarity_percent"`
}

type AISubmissionResult struct {
	SubmissionID  string `json:"id"`
	SourceFile    string `json:"source_file,omitempty"`
	FunctionCount int    `json:"function_count"`
	MatchCount    int    `json:"match_count,omitempty"`
	// Share of the submission's code made of dictionary patterns; the key predates that meaning.
	Score float64 `json:"best_score"`
	// Set by dictionary coverage, a style review flag, or a flagged tell.
	Flagged      bool                `json:"flagged"`
	ParseError   string              `json:"parse_error,omitempty"`
	Matches      []AIDictionaryMatch `json:"matches,omitempty"`
	Style        *AIStyleReport      `json:"style,omitempty"`
	Tells        []AITell            `json:"tells,omitempty"`
	TokenMetrics *AITokenMetrics     `json:"token_metrics,omitempty"`
	AIScore      *AIScoreReport      `json:"ai_score,omitempty"`
	// Related submissions are context only and never propagate flags.
	SimilarToFlagged []AISimilarLink `json:"similar_to_flagged,omitempty"`
}

type AIDetectionReport struct {
	SourceFile           string                   `json:"source_file"`
	DictionaryEntryCount int                      `json:"dictionary_entry_count"`
	DictionaryUsable     int                      `json:"dictionary_usable"`
	DictionaryErrors     []AIDictionaryEntryError `json:"dictionary_errors,omitempty"`
	Submissions          []AISubmissionResult     `json:"submissions"`
}
