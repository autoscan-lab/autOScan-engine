package domain

// Bump this when saved reports need recomputing, including token-model changes.
const AIDetectionMethod = "contextual-v2"

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
	SampleCount   int                `json:"sample_count,omitempty"`
	Reliability   float64            `json:"reliability,omitempty"`
}

type AISourceEvidence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Label     string `json:"label"`
	Snippet   string `json:"snippet,omitempty"`
}

type AIScoreContribution struct {
	Key           string             `json:"key"`
	FeatureKey    string             `json:"feature_key,omitempty"`
	Label         string             `json:"label"`
	Points        float64            `json:"points"`
	Detail        string             `json:"detail"`
	Locations     []AISourceEvidence `json:"locations,omitempty"`
	LocationCount int                `json:"location_count,omitempty"`
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
	Percentile  float64              `json:"percentile"`
	CohortSize  int                  `json:"cohort_size"`
	Flagged     bool                 `json:"flagged"`
	Features    []AIStyleFeature     `json:"features"`
	SourceStats *AISourceStats       `json:"source_stats,omitempty"`
	Formatting  []AIFormattingChoice `json:"formatting,omitempty"`
}

type AISourceStats struct {
	TokenCount    int            `json:"token_count"`
	CodeLines     int            `json:"code_lines"`
	Calls         int            `json:"calls"`
	Conditions    int            `json:"conditions"`
	Arrays        int            `json:"arrays"`
	PatternCounts map[string]int `json:"pattern_counts,omitempty"`
}

type AIFormattingChoice struct {
	Key           string  `json:"key"`
	Label         string  `json:"label"`
	SampleCount   int     `json:"sample_count"`
	Dominant      string  `json:"dominant"`
	DominantCount int     `json:"dominant_count"`
	Entropy       float64 `json:"entropy"`
}

type AITokenWindow struct {
	File                string  `json:"file"`
	StartLine           int     `json:"start_line"`
	EndLine             int     `json:"end_line"`
	TokenCount          int     `json:"token_count"`
	CrossEntropyBits    float64 `json:"cross_entropy_bits"`
	Perplexity          float64 `json:"perplexity"`
	EntropyBits         float64 `json:"entropy_bits"`
	SurprisalStddevBits float64 `json:"surprisal_stddev_bits"`
}

type AITokenMetrics struct {
	TokenCount                 int             `json:"token_count"`
	EntropyBits                float64         `json:"entropy_bits"`
	CohortSize                 int             `json:"cohort_size"`
	CrossEntropyBits           *float64        `json:"cross_entropy_bits,omitempty"`
	Perplexity                 *float64        `json:"perplexity,omitempty"`
	SurprisalStddevBits        *float64        `json:"surprisal_stddev_bits,omitempty"`
	Method                     string          `json:"method,omitempty"`
	ExcludedPeers              int             `json:"excluded_peers,omitempty"`
	NormalizedEntropyBits      *float64        `json:"normalized_entropy_bits,omitempty"`
	NormalizedCrossEntropyBits *float64        `json:"normalized_cross_entropy_bits,omitempty"`
	NormalizedPerplexity       *float64        `json:"normalized_perplexity,omitempty"`
	UnknownTokenShare          *float64        `json:"unknown_token_share,omitempty"`
	WindowStddevBits           *float64        `json:"window_stddev_bits,omitempty"`
	WindowBurstiness           *float64        `json:"window_burstiness,omitempty"`
	AdjacentWindowChangeBits   *float64        `json:"adjacent_window_change_bits,omitempty"`
	WindowCount                int             `json:"window_count,omitempty"`
	Windows                    []AITokenWindow `json:"windows,omitempty"`
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
	Method               string                   `json:"method"`
	SourceFile           string                   `json:"source_file"`
	DictionaryEntryCount int                      `json:"dictionary_entry_count"`
	DictionaryUsable     int                      `json:"dictionary_usable"`
	DictionaryErrors     []AIDictionaryEntryError `json:"dictionary_errors,omitempty"`
	Submissions          []AISubmissionResult     `json:"submissions"`
}
