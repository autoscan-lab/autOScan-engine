package engine

import (
	"fmt"
	"math"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

func scoreRound(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

// Independent evidence sets floors rather than repeatedly counting correlated signals.
func overallAIScore(result domain.AISubmissionResult, dictionaryUsable bool) *domain.AIScoreReport {
	report := &domain.AIScoreReport{Method: domain.AIDetectionMethod, Contributions: []domain.AIScoreContribution{}}
	measured := hasReviewFeatures(result.Style)
	if measured {
		weight := 0.0
		for _, feature := range reviewFeatures {
			weight += feature.weight
		}
		adjustmentIndex := -1
		for _, feature := range result.Style.Features {
			if feature.Evidence != evidenceReview || weight <= 0 {
				continue
			}
			points := scoreRound(100*feature.Score*feature.Reliability*feature.Weight/weight, 2)
			report.Contributions = append(report.Contributions, domain.AIScoreContribution{
				Key: "style:" + feature.Key, FeatureKey: feature.Key, Label: feature.Label,
				Points: points, Detail: feature.Detail,
			})
			report.Score += points
			if points > 0 && (adjustmentIndex < 0 || points > report.Contributions[adjustmentIndex].Points) {
				adjustmentIndex = len(report.Contributions) - 1
			}
		}
		if adjustmentIndex >= 0 {
			target := scoreRound(result.Style.Score*100, 1)
			report.Contributions[adjustmentIndex].Points = scoreRound(report.Contributions[adjustmentIndex].Points+target-report.Score, 2)
			report.Score = target
		}
	}
	addFloor := func(key, label, detail string, floor float64, locations []domain.AISourceEvidence) {
		measured = true
		points := scoreRound(math.Max(0, floor-report.Score), 2)
		report.Contributions = append(report.Contributions, domain.AIScoreContribution{
			Key: key, Label: label, Points: points, Detail: detail, Locations: locations,
		})
		report.Score = scoreRound(report.Score+points, 2)
	}
	if dictionaryUsable && result.ParseError == "" {
		var locations []domain.AISourceEvidence
		for _, match := range result.Matches {
			for _, span := range match.Spans {
				locations = append(locations, domain.AISourceEvidence{File: result.SourceFile, StartLine: span.StartLine, EndLine: span.EndLine, Label: match.Title})
			}
		}
		addFloor("dictionary", "Dictionary patterns", fmt.Sprintf("%.1f%% code coverage; contributes only above the stronger baseline", result.Score*100), scoreRound(result.Score*100, 1), locations)
	}
	addContextualPatterns(report, result)
	addFormattingSupport(report, result.Style)
	for _, kind := range []string{tellKindToolFile, tellKindChatText} {
		var locations []domain.AISourceEvidence
		for _, tell := range result.Tells {
			if !tell.Flagged || tell.Kind != kind {
				continue
			}
			locations = append(locations, domain.AISourceEvidence{File: tell.File, StartLine: tell.Line, EndLine: max(tell.Line, tell.EndLine), Label: tell.Label, Snippet: tell.Snippet})
		}
		if len(locations) == 0 {
			continue
		}
		switch kind {
		case tellKindToolFile:
			if result.Style == nil || result.Style.SourceStats == nil {
				continue
			}
			tokens := result.Style.SourceStats.TokenCount
			floor := 10 * math.Sqrt(float64(tokens)/float64(tokens+100)) * (0.5 + 0.5*float64(min(3, supportedStyleFamilies(result.Style)))/3)
			addFloor("tool_artifact", "AI tool configuration", fmt.Sprintf("Configuration presence across %d parsed tokens; context-weighted floor at most 10 points, with no duplicate-file bonuses", tokens), floor, locations)
		case tellKindChatText:
			addFloor("explicit_attribution", "Explicit AI attribution", "90-point floor for generator attribution or model self-identification; attribution can be edited", 90, locations)
		}
	}
	if !measured && len(report.Contributions) == 0 {
		return nil
	}
	target := scoreRound(report.Score, 1)
	best := -1
	for i, entry := range report.Contributions {
		if entry.Points > 0 && (best < 0 || entry.Points > report.Contributions[best].Points) {
			best = i
		}
	}
	if best >= 0 {
		report.Contributions[best].Points = scoreRound(report.Contributions[best].Points+target-report.Score, 2)
	}
	report.Score = target
	return report
}

func supportedStyleFamilies(style *domain.AIStyleReport) int {
	if style == nil {
		return 0
	}
	families := map[string]bool{}
	for _, feature := range style.Features {
		if feature.Evidence != evidenceReview || feature.Score*feature.Reliability < 0.5 {
			continue
		}
		family := feature.Key
		if family == "prose_comments" || family == "doc_headers" {
			family = "comments"
		}
		families[family] = true
	}
	return len(families)
}

// Related patterns share a budget and use structural opportunities rather than physical lines.
func addContextualPatterns(report *domain.AIScoreReport, result domain.AISubmissionResult) {
	if result.Style == nil || result.Style.SourceStats == nil {
		return
	}
	stats := result.Style.SourceStats
	corroboration := 0.5 + 0.5*float64(min(3, supportedStyleFamilies(result.Style)))/3
	type candidate struct {
		key, label                 string
		occurrences, opportunities int
		strength                   float64
		locations                  []domain.AISourceEvidence
	}
	var candidates []candidate
	for _, rule := range []struct {
		key, label    string
		cap           float64
		opportunities int
	}{
		{"initialized_array", "Initialized arrays", 40, stats.Arrays},
		{"variable_length_array", "Variable-length arrays", 30, stats.Arrays},
		{tellKindWrappedCall, "Long calls wrapped across lines", 30, stats.Calls},
		{tellKindComplexCondition, "Complex multiline conditions", 20, stats.Conditions},
	} {
		n := stats.PatternCounts[rule.key]
		if n == 0 {
			continue
		}
		strength := rule.cap * float64(n) / float64(n+2) * math.Sqrt(float64(n)/float64(max(n, rule.opportunities)+5)) *
			math.Sqrt(float64(stats.TokenCount)/float64(stats.TokenCount+100)) *
			math.Sqrt(math.Min(1, float64(100*n)/float64(max(1, stats.TokenCount)))) * corroboration
		entry := candidate{key: rule.key, label: rule.label, occurrences: n, opportunities: rule.opportunities, strength: strength}
		for _, tell := range result.Tells {
			if tell.Kind == rule.key {
				entry.locations = append(entry.locations, domain.AISourceEvidence{File: tell.File, StartLine: tell.Line, EndLine: max(tell.Line, tell.EndLine), Label: tell.Label, Snippet: tell.Snippet})
			}
		}
		candidates = append(candidates, entry)
	}
	arrayStrength := 0.0
	for _, entry := range candidates {
		if entry.key == "initialized_array" || entry.key == "variable_length_array" {
			arrayStrength = math.Max(arrayStrength, entry.strength)
		}
	}
	arraySum, total := 0.0, 0.0
	for _, entry := range candidates {
		if entry.key == "initialized_array" || entry.key == "variable_length_array" {
			arraySum += entry.strength
		} else {
			total += entry.strength
		}
	}
	total += arrayStrength
	budgetScale := math.Min(1, 24/math.Max(24, total)) * (1 - report.Score/100)
	for _, entry := range candidates {
		strength := entry.strength
		if arraySum > 0 && (entry.key == "initialized_array" || entry.key == "variable_length_array") {
			strength *= arrayStrength / arraySum
		}
		points := scoreRound(strength*budgetScale, 2)
		report.Contributions = append(report.Contributions, domain.AIScoreContribution{Key: entry.key, Label: entry.label,
			Points: points, Locations: entry.locations, LocationCount: entry.occurrences,
			Detail: fmt.Sprintf("%d of %d relevant constructs across %d parsed tokens; repetition, prevalence, token density, sample size and %d supporting style families. Related patterns share a 24-point budget, reduced by the existing score.", entry.occurrences, entry.opportunities, stats.TokenCount, supportedStyleFamilies(result.Style))})
		report.Score += points
	}
	report.Score = scoreRound(report.Score, 2)
}

func addFormattingSupport(report *domain.AIScoreReport, style *domain.AIStyleReport) {
	families := supportedStyleFamilies(style)
	if families < 2 || style.SourceStats == nil {
		return
	}
	n, samples, sum := 0, 0, 0.0
	for _, choice := range style.Formatting {
		if choice.SampleCount < 20 {
			continue
		}
		n++
		samples += choice.SampleCount
		sum += float64(choice.DominantCount) / float64(choice.SampleCount)
	}
	if n < 4 {
		return
	}
	consistency := sum / float64(n)
	strength := math.Max(0, (consistency-0.95)/0.05) * sampleSupport(samples, 200) * sampleSupport(style.SourceStats.TokenCount, 400)
	points := scoreRound(4*strength*float64(min(3, families))/3*(1-report.Score/100), 2)
	report.Contributions = append(report.Contributions, domain.AIScoreContribution{Key: "formatting_support", Label: "Consistent formatting with supporting signals", Points: points,
		Detail: fmt.Sprintf("%.1f%% mean consistency across %d contexts and %d observations, with %d supporting style families. At most 4 points; formatting alone adds none.", consistency*100, n, samples, families)})
	report.Score = scoreRound(report.Score+points, 2)
}
