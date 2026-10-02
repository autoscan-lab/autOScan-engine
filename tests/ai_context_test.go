package tests

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

func contextReport(t *testing.T, submissions []domain.Submission) map[string]domain.AISubmissionResult {
	t.Helper()
	report, err := engine.ComputeAIDetectionFromFingerprints(submissions, engine.FingerprintSubmissions(submissions, "lab.c", similarityConfig), "lab.c", nil, similarityConfig)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]domain.AISubmissionResult{}
	for _, result := range report.Submissions {
		results[result.SubmissionID] = result
	}
	return results
}

func diverseMetricSources(t *testing.T, root string) []domain.Submission {
	t.Helper()
	var submissions []domain.Submission
	for i := 0; i < 5; i++ {
		source := studentStyled + "\nint extra(int value) { return value" + strings.Repeat(" + value", i+1) + "; }\n"
		submissions = append(submissions, writeStyleSubmission(t, root, fmt.Sprint(i), map[string]string{"lab.c": source}))
	}
	return submissions
}

func contributionPoints(result domain.AISubmissionResult, key string) float64 {
	if result.AIScore != nil {
		for _, entry := range result.AIScore.Contributions {
			if entry.Key == key {
				return entry.Points
			}
		}
	}
	return 0
}

func TestContextualPatternsWeighRepetitionOpportunitiesAndCodeSize(t *testing.T) {
	makeSource := func(initialized, plain, filler int) string {
		var source strings.Builder
		source.WriteString("int run(int seed) {\n")
		for i := 0; i < initialized; i++ {
			fmt.Fprintf(&source, "char item%d[] = {'a', 'b'};\n", i)
		}
		for i := 0; i < plain; i++ {
			fmt.Fprintf(&source, "char other%d[2];\n", i)
		}
		for i := 0; i < filler; i++ {
			source.WriteString("seed = seed + 1;\n")
		}
		source.WriteString("return seed;\n}\n")
		return source.String()
	}
	one := scoredSubmission(t, map[string]string{"lab.c": makeSource(1, 0, 20)})
	repeated := scoredSubmission(t, map[string]string{"lab.c": makeSource(5, 0, 20)})
	diluted := scoredSubmission(t, map[string]string{"lab.c": makeSource(5, 30, 20)})
	large := scoredSubmission(t, map[string]string{"lab.c": makeSource(1, 0, 400)})
	if !(contributionPoints(repeated, "initialized_array") > contributionPoints(one, "initialized_array") &&
		contributionPoints(diluted, "initialized_array") < contributionPoints(repeated, "initialized_array") &&
		contributionPoints(large, "initialized_array") < contributionPoints(one, "initialized_array")) {
		t.Fatalf("context ordering failed: one %.2f repeated %.2f diluted %.2f large %.2f", contributionPoints(one, "initialized_array"), contributionPoints(repeated, "initialized_array"), contributionPoints(diluted, "initialized_array"), contributionPoints(large, "initialized_array"))
	}
	for _, result := range []domain.AISubmissionResult{one, repeated, diluted, large} {
		if !result.Flagged || result.AIScore.Score >= 40 {
			t.Fatalf("course review evidence lost or old floor retained: %+v", result.AIScore)
		}
		assertContributionTotal(t, result)
	}
	packed := scoredSubmission(t, map[string]string{"lab.c": strings.ReplaceAll(makeSource(1, 0, 20), "\n", " ")})
	if contributionPoints(packed, "initialized_array") != contributionPoints(one, "initialized_array") {
		t.Fatal("physical line wrapping changed pattern evidence")
	}
}

func assertContributionTotal(t *testing.T, result domain.AISubmissionResult) {
	t.Helper()
	total, patterns := 0.0, 0.0
	for _, entry := range result.AIScore.Contributions {
		if entry.Points < 0 || math.IsNaN(entry.Points) || math.IsInf(entry.Points, 0) {
			t.Fatal("invalid contribution")
		}
		total += entry.Points
		if entry.Key == "initialized_array" || entry.Key == "variable_length_array" || entry.Key == "wrapped_call" || entry.Key == "complex_condition" {
			patterns += entry.Points
		}
	}
	if math.Abs(total-result.AIScore.Score) > 0.001 || result.AIScore.Score > 100 || patterns > 24.05 {
		t.Fatalf("invalid bounded score %+v", result.AIScore)
	}
}

func TestPatternCountsSurviveCappedExamples(t *testing.T) {
	var source strings.Builder
	source.WriteString("int run(int size) {\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&source, "char item%d[] = {'a'};\n", i)
	}
	source.WriteString("return size;\n}\n")
	result := scoredSubmission(t, map[string]string{"lab.c": source.String()})
	if result.Style.SourceStats.Arrays != 80 || result.Style.SourceStats.PatternCounts["initialized_array"] != 80 {
		t.Fatal("capped examples reduced measurement counts")
	}
	for _, entry := range result.AIScore.Contributions {
		if entry.Key == "initialized_array" && (entry.LocationCount != 80 || len(entry.Locations) != 64) {
			t.Fatalf("full count missing: %+v", entry)
		}
	}
	assertContributionTotal(t, result)
}

func TestExactFormattingSeparatesSpacesAndSyntaxContexts(t *testing.T) {
	var source strings.Builder
	source.WriteString("int run(int value) {\n")
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			source.WriteString("value = value+1;\n")
		} else {
			source.WriteString("value    =    value+1;\n")
		}
		source.WriteString("if (value==0) { value = 1; }\n")
	}
	source.WriteString("return value;\n}\n")
	result := scoredSubmission(t, map[string]string{"lab.c": source.String()})
	choices := map[string]domain.AIFormattingChoice{}
	for _, choice := range result.Style.Formatting {
		choices[choice.Key] = choice
	}
	assignment, comparison := choices["assignment_gap"], choices["comparison_gap"]
	if assignment.SampleCount != 20 || assignment.DominantCount != 15 || assignment.Entropy <= 0 || assignment.Dominant != "1 spaces / 1 spaces" {
		t.Fatalf("exact spacing not measured: %+v", assignment)
	}
	if comparison.SampleCount != 10 || comparison.Dominant != "0 spaces / 0 spaces" || comparison.Entropy != 0 {
		t.Fatalf("unrelated syntax mixed: %+v", comparison)
	}
	if contributionPoints(result, "formatting_support") != 0 {
		t.Fatal("uniform formatting alone raised score")
	}
}

func TestReviewFeaturesHaveSampleSupportAndFixedFamilyBudget(t *testing.T) {
	makeSource := func(n int) string {
		var source strings.Builder
		source.WriteString("int run(int value) {\n")
		for i := 0; i < n; i++ {
			source.WriteString("// This sentence explains the next operation.\nvalue = value + 1;\n")
		}
		source.WriteString("return value;\n}\n")
		return source.String()
	}
	small, large := scoredSubmission(t, map[string]string{"lab.c": makeSource(3)}), scoredSubmission(t, map[string]string{"lab.c": makeSource(30)})
	smallFeature, largeFeature := styleFeature(t, small.Style, "prose_comments"), styleFeature(t, large.Style, "prose_comments")
	if smallFeature.Value != largeFeature.Value || smallFeature.Reliability >= largeFeature.Reliability || smallFeature.Score != largeFeature.Score || contributionPoints(small, "style:prose_comments") >= contributionPoints(large, "style:prose_comments") {
		t.Fatal("sample size not reflected in review evidence")
	}
	if large.AIScore.Score > 35 || large.Flagged {
		t.Fatalf("one measured family was normalized to certainty: %+v", large.AIScore)
	}
}

func TestNormalizedMetricsIgnoreRenamingAndDuplicatePeers(t *testing.T) {
	root := t.TempDir()
	submissions := diverseMetricSources(t, root)
	base := contextReport(t, submissions)["0"].TokenMetrics
	if base == nil || base.NormalizedPerplexity == nil {
		t.Fatal("distinct peers should produce metrics")
	}
	rename := strings.ReplaceAll(studentStyled+"\nint extra(int value) { return value + value; }\n", "escriu", "brand_new_spelling")
	renamed := append([]domain.Submission(nil), submissions...)
	renamed[0] = writeStyleSubmission(t, root, "renamed", map[string]string{"lab.c": rename})
	changed := contextReport(t, renamed)["renamed"].TokenMetrics
	if *base.NormalizedPerplexity != *changed.NormalizedPerplexity || *base.NormalizedEntropyBits != *changed.NormalizedEntropyBits {
		t.Fatal("identifier renaming changed structural metrics")
	}
	if *base.Perplexity == *changed.Perplexity || changed.UnknownTokenShare == nil || *changed.UnknownTokenShare == 0 {
		t.Fatal("raw naming difference should remain inspectable")
	}
	copySource := studentStyled + "\nint extra(int value) { return value + value + value; }\n"
	duplicate := writeStyleSubmission(t, root, "duplicate", map[string]string{"lab.c": copySource})
	withDuplicate := contextReport(t, append(submissions, duplicate))["0"].TokenMetrics
	if *base.Perplexity != *withDuplicate.Perplexity || *base.NormalizedPerplexity != *withDuplicate.NormalizedPerplexity || withDuplicate.CohortSize != base.CohortSize || withDuplicate.ExcludedPeers != 1 {
		t.Fatalf("duplicate skewed model: %+v", withDuplicate)
	}
	for i, j := 0, len(submissions)-1; i < j; i, j = i+1, j-1 {
		submissions[i], submissions[j] = submissions[j], submissions[i]
	}
	if *contextReport(t, submissions)["0"].TokenMetrics.NormalizedPerplexity != *base.NormalizedPerplexity {
		t.Fatal("input ordering changed model")
	}
}

func TestMetricsAbstainForEquivalentCohortAndSmallSamples(t *testing.T) {
	root := t.TempDir()
	var submissions []domain.Submission
	for i := 0; i < 6; i++ {
		submissions = append(submissions, writeStyleSubmission(t, root, fmt.Sprint(i), map[string]string{"lab.c": strings.ReplaceAll(studentStyled, "escriu", fmt.Sprintf("renamed_%d", i))}))
	}
	for _, result := range contextReport(t, submissions) {
		if result.TokenMetrics.Perplexity != nil || result.TokenMetrics.CohortSize != 0 || result.TokenMetrics.ExcludedPeers != 5 {
			t.Fatalf("equivalent code leaked into target model: %+v", result.TokenMetrics)
		}
	}
	short := writeStyleSubmission(t, root, "small", map[string]string{"lab.c": "int run(int n) { " + strings.Repeat("n += 1; ", 12) + "return n; }"})
	result := contextReport(t, append(diverseMetricSources(t, root), short))["small"]
	if result.TokenMetrics == nil || result.TokenMetrics.TokenCount >= 128 || result.TokenMetrics.Perplexity != nil || len(result.TokenMetrics.Windows) != 0 {
		t.Fatal("small sample should expose entropy but abstain from peer likelihood")
	}
}

func TestLocalProfilesPreserveSourceBoundariesAndBoundOutput(t *testing.T) {
	root := t.TempDir()
	source := "int sequence(int seed) {\n" + strings.Repeat("seed = seed + 1;\n", 400) + strings.Repeat("seed = (seed << 1) ^ (seed >> 3);\n", 400) + "return seed;\n}\n"
	target := writeStyleSubmission(t, root, "profile", map[string]string{"lab.c": source, "helper.c": "int helper(int x) {\n" + strings.Repeat("x = x - 1;\n", 40) + "return x;\n}\n"})
	result := contextReport(t, append(diverseMetricSources(t, root), target))["profile"]
	m := result.TokenMetrics
	if m.WindowCount <= 32 || len(m.Windows) != 32 || m.WindowStddevBits == nil || m.AdjacentWindowChangeBits == nil || *m.AdjacentWindowChangeBits <= 0 || m.WindowBurstiness == nil || *m.WindowBurstiness < -1 || *m.WindowBurstiness > 1 {
		t.Fatalf("local variation missing or unbounded: %+v", m)
	}
	for _, window := range m.Windows {
		lines := 803
		if window.File == "helper.c" {
			lines = 43
		} else if window.File != "lab.c" {
			t.Fatal("unknown file")
		}
		if window.StartLine < 1 || window.EndLine < window.StartLine || window.EndLine > lines || window.TokenCount < 64 || window.EntropyBits <= 0 || window.SurprisalStddevBits < 0 || math.Abs(math.Log2(window.Perplexity)-window.CrossEntropyBits) > 0.002 {
			t.Fatalf("invalid profile %+v", window)
		}
	}
	assertContributionTotal(t, result)
}

func TestPreprocessorScaffoldingDoesNotEraseConditionalCode(t *testing.T) {
	root := t.TempDir()
	plain := writeStyleSubmission(t, root, "plain", map[string]string{"lab.c": studentStyled})
	guarded := writeStyleSubmission(t, root, "guarded", map[string]string{"lab.c": "#define GENERATED_CONSTANT 100\n#ifdef INCLUDED\n" + studentStyled + "\n#endif\n"})
	results := contextReport(t, []domain.Submission{plain, guarded})
	if results["plain"].TokenMetrics.TokenCount != results["guarded"].TokenMetrics.TokenCount || *results["plain"].TokenMetrics.NormalizedEntropyBits != *results["guarded"].TokenMetrics.NormalizedEntropyBits {
		t.Fatal("directive content leaked or conditional C was removed")
	}
}
