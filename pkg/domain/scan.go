package domain

type BannedHit struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
}

func NewBannedHit(function, file string, line, column int, snippet string) BannedHit {
	return BannedHit{Function: function, File: file, Line: line, Column: column, Snippet: snippet}
}

type ScanResult struct {
	Hits           []BannedHit            `json:"hits,omitempty"`
	HitsByFunction map[string][]BannedHit `json:"-"`
	ParseErrors    []string               `json:"parse_errors,omitempty"`
}

func NewScanResult(hits []BannedHit, parseErrors []string) ScanResult {
	byFunc := make(map[string][]BannedHit)
	for _, h := range hits {
		byFunc[h.Function] = append(byFunc[h.Function], h)
	}
	return ScanResult{Hits: hits, HitsByFunction: byFunc, ParseErrors: parseErrors}
}

func (s ScanResult) TotalHits() int { return len(s.Hits) }
