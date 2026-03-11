package domain

type BannedHit struct {
	Function string
	File     string
	Line     int
	Column   int
	Snippet  string
}

func NewBannedHit(function, file string, line, column int, snippet string) BannedHit {
	return BannedHit{Function: function, File: file, Line: line, Column: column, Snippet: snippet}
}

type ScanResult struct {
	Hits           []BannedHit
	HitsByFunction map[string][]BannedHit
	ParseErrors    []string
}

func NewScanResult(hits []BannedHit, parseErrors []string) ScanResult {
	byFunc := make(map[string][]BannedHit)
	for _, h := range hits {
		byFunc[h.Function] = append(byFunc[h.Function], h)
	}
	return ScanResult{Hits: hits, HitsByFunction: byFunc, ParseErrors: parseErrors}
}

func (s ScanResult) TotalHits() int { return len(s.Hits) }
