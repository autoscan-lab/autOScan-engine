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
	report := &domain.AIScoreReport{Method: "heuristic-v1", Contributions: []domain.AIScoreContribution{}}
	measured := hasReviewFeatures(result.Style)
	if measured {
		weight := 0.0
		for _, feature := range result.Style.Features {
			if feature.Evidence == evidenceReview {
				weight += feature.Weight
			}
		}
		adjustmentIndex := -1
		for _, feature := range result.Style.Features {
			if feature.Evidence != evidenceReview || weight <= 0 {
				continue
			}
			points := scoreRound(100*feature.Score*feature.Weight/weight, 2)
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
		report.Score = scoreRound(report.Score+points, 1)
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
	for _, kind := range []string{tellKindComplexCondition, tellKindWrappedCall, "variable_length_array", "initialized_array", tellKindToolFile, tellKindChatText} {
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
		case "variable_length_array":
			addFloor("variable_length_array", "Variable-length arrays", "30-point floor for runtime-sized arrays; course-specific review rule", 30, locations)
		case "initialized_array":
			addFloor("initialized_array", "Initialized arrays", "40-point floor for literal-initialized arrays; course-specific review rule", 40, locations)
		case tellKindComplexCondition:
			addFloor("complex_condition", "Complex multiline conditions", "20-point floor for a condition spanning at least three lines with at least three &&/|| operators", 20, locations)
		case tellKindWrappedCall:
			addFloor("wrapped_call", "Long calls wrapped across lines", "30-point floor for a multiline argument list whose call is at least 100 characters when whitespace is collapsed", 30, locations)
		case tellKindToolFile:
			addFloor("tool_artifact", "AI tool configuration", "40-point floor for configuration presence; does not establish generated code", 40, locations)
		case tellKindChatText:
			addFloor("explicit_attribution", "Explicit AI attribution", "90-point floor for generator attribution or model self-identification; attribution can be edited", 90, locations)
		}
	}
	if !measured {
		return nil
	}
	return report
}
