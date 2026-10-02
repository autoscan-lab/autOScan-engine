package tests

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

const assistantStyled = `/*
 * Lab 3 - Forks
 */

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

#define CHILD_COUNT 3

/* Writes the whole string to the given file descriptor, retrying on partial writes. */
static int write_all(int fd, const char *text) {
    size_t length = strlen(text);
    size_t written = 0;

    while (written < length) {
        ssize_t result = write(fd, text + written, length - written);
        if (result < 0) {
            if (errno == EINTR) {
                continue;
            }
            return -1;
        }
        written += (size_t)result;
    }
    return 0;
}

/* Returns a random integer in the range [0, 99]. */
static int roll_percent(void) {
    return rand() % 100;
}

/* Reports the outcome of one child process to standard output. */
static void report_child(int index, int roll) {
    char message[128];
    int length = snprintf(message, sizeof(message), "Child %d rolled %d.\n", index, roll);

    if (length < 0) {
        perror("snprintf");
        return;
    }
    if (write_all(STDOUT_FILENO, message) < 0) {
        perror("write");
    }
}

/* Creates the pipe used to send results back to the parent process. */
static int open_channel(int fds[2]) {
    if (pipe(fds) < 0) {
        perror("pipe");
        return -1;
    }
    return 0;
}

/* Starts every child, then waits until all of them have finished. */
int main(void) {
    int fds[2];

    if (open_channel(fds) < 0) {
        return EXIT_FAILURE;
    }
    char *label = malloc(16);
    if (label == NULL) {
        perror("malloc");
        return EXIT_FAILURE;
    }
    free(label);
    for (int i = 0; i < CHILD_COUNT; i++) {
        pid_t pid = fork();
        if (pid < 0) {
            perror("fork");
            return EXIT_FAILURE;
        }
        if (pid == 0) {
            report_child(i, roll_percent());
            exit(EXIT_SUCCESS);
        }
    }
    while (wait(NULL) > 0) {
        /* Keep reaping until no children are left. */
    }
    return EXIT_SUCCESS;
}
`

const studentStyled = `// practica 3
// login: alumne.prova

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/wait.h>

void escriu(char *s){
  write(1,s,strlen(s));
}

int tira() {
    return rand()%100;
}

void fill(int i,int r) {
    char buff[100];
    sprintf(buff, "Child %d rolled %d.\n", i, r);
    escriu(buff);
}

int main(){
    int fd[2], i;
    char *nom = malloc(20);
    pipe(fd);
    strcpy(nom, "galaxia");
    // fem els fills
    for(i=0;i<3;i++){
        int pid = fork();
        if (pid==0) {
            fill(i, tira());
            exit(0);
        }
        else if(pid<0) escriu("error fork\n");
    }
    // esperem
    while(wait(NULL)>0);
    return 0;
}
`

func writeStyleSubmission(t *testing.T, root, id string, files map[string]string) domain.Submission {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(id))
	var cFiles []string
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(name, "/") && strings.HasSuffix(name, ".c") {
			cFiles = append(cFiles, name)
		}
	}
	return domain.Submission{ID: id, Path: dir, CFiles: cFiles}
}

func styleFeature(t *testing.T, report *domain.AIStyleReport, key string) domain.AIStyleFeature {
	t.Helper()
	for _, f := range report.Features {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("feature %q missing from %+v", key, report.Features)
	return domain.AIStyleFeature{}
}

func TestStyleRanksContrastingSyntheticExamples(t *testing.T) {
	root := t.TempDir()
	ai, aiTells := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, root, "ai", map[string]string{"lab.c": assistantStyled}))
	student, studentTells := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, root, "student", map[string]string{"lab.c": studentStyled}))

	if !ai.Flagged || ai.Score < 0.7 {
		t.Fatalf("assistant-styled code scored %.2f (flagged=%v): %+v", ai.Score, ai.Flagged, ai.Features)
	}
	if student.Flagged || student.Score > 0.2 {
		t.Fatalf("student-styled code scored %.2f (flagged=%v): %+v", student.Score, student.Flagged, student.Features)
	}
	if len(aiTells) != 0 || len(studentTells) != 0 {
		t.Fatalf("neither file has tells, got %+v and %+v", aiTells, studentTells)
	}

	// The login header is the student's own and never counts as a comment.
	if f := styleFeature(t, student, "prose_comments"); f.Value != 0 {
		t.Fatalf("student fragments counted as prose: %+v", f)
	}
	if f := styleFeature(t, ai, "error_checks"); f.Value != 1 {
		t.Fatalf("every checkable call in the assistant code is checked, got %+v", f)
	}
	if f := styleFeature(t, student, "error_checks"); f.Value != 0.25 || f.Score != 0 {
		t.Fatalf("only the wait result should be checked, below the review ramp: %+v", f)
	}
}

func TestErrorChecksFollowStoredResults(t *testing.T) {
	src := `#include <unistd.h>
#include <stdlib.h>

int main(void) {
    char buf[8];
    int n = read(0, buf, 8);
    if (n < 0) return 1;
    int m;
    m = write(1, buf, n);
    if (!m) return 1;
    write(1, buf, n);
    char *p = malloc(4);
    free(p);
    return 0;
}
`
	report, _ := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, t.TempDir(), "s", map[string]string{"lab.c": src}))
	if f := styleFeature(t, report, "error_checks"); !strings.HasPrefix(f.Detail, "2 of 4 write/read/pipe/malloc-style calls check the result") {
		t.Fatalf("got %q", f.Detail)
	}
}

func TestFormattingChangesCannotHideStyleEvidence(t *testing.T) {
	messy := strings.NewReplacer(
		"if (result < 0) {", "if(result<0){",
		"while (written < length) {", "while(written<length)\n    {",
		"size_t written = 0;", "size_t written=0;  ",
		"    return rand() % 100;", "  return rand()%100;",
		"if (length < 0) {", "if (length<0)\n    {",
		"perror(\"pipe\");", "perror (\"pipe\");",
	).Replace(assistantStyled)

	root := t.TempDir()
	clean, _ := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, root, "clean", map[string]string{"lab.c": assistantStyled}))
	hand, _ := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, root, "messy", map[string]string{"lab.c": messy}))
	if f := styleFeature(t, hand, "format_entropy"); f.Evidence != "context" || f.Score == 0 {
		t.Fatalf("messy formatting should be context, got %+v", f)
	}
	if hand.Score != clean.Score {
		t.Fatalf("messy copy scored %.2f, clean %.2f; formatting entropy must not change the score", hand.Score, clean.Score)
	}
}

func TestTellsFindChatTextAndTypography(t *testing.T) {
	src := "#include <stdio.h>\n\n" +
		"// Here's the updated version of the function — now with checks.\n" +
		"int main(void) {\n" +
		"    // ... existing code ...\n" +
		"    printf(\"done​\\n\");\n" +
		"    // Catalan and Spanish letters are fine: col·legi, niño, àéí\n" +
		"    return 0;\n" +
		"}\n"
	_, tells := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, t.TempDir(), "s", map[string]string{"lab.c": src}))

	var got []string
	for _, tell := range tells {
		if tell.File != "lab.c" {
			t.Fatalf("tell should name its file, got %+v", tell)
		}
		got = append(got, fmt.Sprintf("%d %s: %s", tell.Line, tell.Kind, tell.Label))
	}
	want := []string{
		"3 chat_text: Chat assistant wording in a comment",
		"3 unicode: Em or en dash (— –) in a comment",
		"5 chat_text: Chat assistant wording in a comment",
		"6 unicode: Invisible character (zero-width or non-breaking space)",
	}
	// Accented letters, ñ and l·l on line 7 must not be tells.
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTellsFindToolFilesAboveTheSourceFolder(t *testing.T) {
	root := t.TempDir()
	sub := writeStyleSubmission(t, root, "Student_1_assignsubmission_file/project/src", map[string]string{"lab.c": studentStyled})
	for _, extra := range []string{"project/.cursor/rules.mdc", "project/.github/copilot-instructions.md", "CLAUDE.md"} {
		path := filepath.Join(root, "Student_1_assignsubmission_file", filepath.FromSlash(extra))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Another student's files must never be attributed to this one.
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, tells := engine.AnalyzeSubmissionStyle(sub)
	labels := map[string]bool{}
	for _, tell := range tells {
		if tell.Kind != "tool_file" {
			t.Fatalf("unexpected tell %+v", tell)
		}
		labels[tell.Label] = true
	}
	for _, want := range []string{"Cursor project folder", "GitHub Copilot instructions file", "Claude Code instructions file"} {
		if !labels[want] {
			t.Fatalf("missing %q in %+v", want, tells)
		}
	}
	if len(tells) != 3 {
		t.Fatalf("want exactly the student's 3 tool files, got %+v", tells)
	}
}

func TestAIDetectionRunsStyleWithoutDictionary(t *testing.T) {
	root := t.TempDir()
	subs := []domain.Submission{
		writeStyleSubmission(t, root, "ai", map[string]string{"lab.c": assistantStyled}),
		writeStyleSubmission(t, root, "student", map[string]string{"lab.c": studentStyled}),
	}
	prints := engine.FingerprintSubmissions(subs, "lab.c", similarityConfig)
	report, err := engine.ComputeAIDetectionFromFingerprints(subs, prints, "lab.c", nil, similarityConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Submissions) != 2 || report.Submissions[0].SubmissionID != "ai" {
		t.Fatalf("flagged submission should sort first, got %+v", report.Submissions)
	}
	first, second := report.Submissions[0], report.Submissions[1]
	if !first.Flagged || first.Score != 0 || first.Style.Percentile != 1 {
		t.Fatalf("style alone should flag without dictionary coverage: %+v", first)
	}
	if second.Flagged || second.Style.Percentile != 0 {
		t.Fatalf("student code should not be flagged: %+v", second)
	}
}

func TestSimilarToFlaggedLinksOneStep(t *testing.T) {
	report := domain.AIDetectionReport{Submissions: []domain.AISubmissionResult{
		{SubmissionID: "seed", Score: 0.3, Flagged: true, Style: &domain.AIStyleReport{}},
		{SubmissionID: "copy", Style: &domain.AIStyleReport{}},
		{SubmissionID: "copy-of-copy", Style: &domain.AIStyleReport{}},
		{SubmissionID: "loose", Style: &domain.AIStyleReport{}},
	}}
	pair := func(a, b string, percent float64, flagged bool) domain.SimilarityPairResult {
		return domain.SimilarityPairResult{A: a, B: b, PlagiarismResult: domain.PlagiarismResult{SimilarityPercent: percent, Flagged: flagged}}
	}
	engine.LinkSimilarToFlagged(&report, &domain.SimilarityReport{Pairs: []domain.SimilarityPairResult{
		pair("copy", "seed", 82, true),
		pair("copy", "copy-of-copy", 90, true),
		pair("seed", "loose", 40, false),
	}})

	got := map[string]domain.AISubmissionResult{}
	for _, sub := range report.Submissions {
		got[sub.SubmissionID] = sub
	}
	if c := got["copy"]; c.Flagged || len(c.SimilarToFlagged) != 1 || c.SimilarToFlagged[0].SubmissionID != "seed" || c.SimilarToFlagged[0].SimilarityPercent != 82 {
		t.Fatalf("copy should link without inheriting a flag: %+v", c)
	}
	if c := got["copy-of-copy"]; c.Flagged || len(c.SimilarToFlagged) != 0 {
		t.Fatalf("links must not chain through a linked submission: %+v", c)
	}
	if c := got["loose"]; c.Flagged {
		t.Fatalf("an unflagged similarity pair must not link: %+v", c)
	}
	if c := got["seed"]; len(c.SimilarToFlagged) != 0 {
		t.Fatalf("seed has no flagged neighbour of its own: %+v", c)
	}
}

func TestTypographyAndGenericWordingAreContextOnly(t *testing.T) {
	for _, source := range []string{
		"// Calculation → result…\nint main(void) { return 0; }",
		"// I've added the missing result check.\nint main(void) { return 0; }",
		"// Let me know if anything fails.\nint main(void) { return 0; }",
	} {
		sub := writeStyleSubmission(t, t.TempDir(), "s", map[string]string{"lab.c": source})
		prints := engine.FingerprintSubmissions([]domain.Submission{sub}, "lab.c", similarityConfig)
		report, err := engine.ComputeAIDetectionFromFingerprints([]domain.Submission{sub}, prints, "lab.c", nil, similarityConfig)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Submissions[0].Tells) == 0 || report.Submissions[0].Flagged {
			t.Fatalf("context must not flag: %+v", report)
		}
	}
}

func TestExplicitProvenanceAndSecondaryFiles(t *testing.T) {
	sub := writeStyleSubmission(t, t.TempDir(), "s", map[string]string{
		"lab.c":             "int main(void) { return 0; }",
		"helper.c":          "// As an AI language model, I cannot run this code.\nint helper(void) { return 1; }",
		".cursor/rules.mdc": "project instructions",
	})
	report, err := engine.ComputeAIDetectionFromFingerprints([]domain.Submission{sub}, engine.FingerprintSubmissions([]domain.Submission{sub}, "lab.c", similarityConfig), "lab.c", nil, similarityConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Submissions[0].Flagged {
		t.Fatal("explicit provenance should raise a review flag")
	}
	found := false
	for _, tell := range report.Submissions[0].Tells {
		if tell.File == "helper.c" && tell.Flagged {
			found = true
		}
	}
	if !found {
		t.Fatal("secondary source was not inspected")
	}
}

func TestAssistantAttributionAvoidsOrdinaryAIReferences(t *testing.T) {
	for _, test := range []struct {
		comment string
		flagged bool
	}{
		{"// Generated by ChatGPT", true},
		{"/* Code written with Claude */", true},
		{"// As an AI student, I implemented this myself.", false},
		{"// This was not generated by ChatGPT.", false},
		{"// Detect code generated by ChatGPT.", false},
	} {
		_, tells := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, t.TempDir(), "s", map[string]string{"lab.c": test.comment + "\nint main(void) { return 0; }"}))
		flagged := false
		for _, tell := range tells {
			flagged = flagged || tell.Flagged
		}
		if flagged != test.flagged {
			t.Fatalf("%q: flagged=%v, want %v", test.comment, flagged, test.flagged)
		}
	}
}

func TestStyleDoesNotTreatDiscardedOrOverwrittenResultsAsChecks(t *testing.T) {
	source := `#include <unistd.h>
int main(void) {
  char b[8];
  (void)read(0, b, 8);
  int n = read(0, b, 8);
  n = 0;
  if (n < 0) return 1;
  int m = read(0, b, 8);
  { int m = 0; if (m < 0) return 1; }
  return 0;
}`
	report, _ := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, t.TempDir(), "s", map[string]string{"lab.c": source}))
	if f := styleFeature(t, report, "error_checks"); f.Value != 0 {
		t.Fatalf("discarded, overwritten, and shadowed values are not checked: %+v", f)
	}
}

func TestMalformedCodeAndMismatchedFingerprints(t *testing.T) {
	sub := writeStyleSubmission(t, t.TempDir(), "bad", map[string]string{"lab.c": "int main( {"})
	style, _ := engine.AnalyzeSubmissionStyle(sub)
	if style != nil {
		t.Fatal("malformed source must not supply style evidence")
	}
	if _, err := engine.ComputeAIDetectionFromFingerprints([]domain.Submission{sub}, nil, "lab.c", nil, similarityConfig); err == nil {
		t.Fatal("mismatched fingerprints must return an error")
	}
	report, err := engine.ComputeAIDetectionFromFingerprints([]domain.Submission{sub}, engine.FingerprintSubmissions([]domain.Submission{sub}, "lab.c", similarityConfig), "lab.c", nil, similarityConfig)
	if err != nil || report.Submissions[0].Flagged {
		t.Fatalf("malformed source should not panic or flag: %+v %v", report, err)
	}
}

func TestCommentsAloneCannotRaiseStyleFlag(t *testing.T) {
	source := "#include <stdio.h>\n"
	for i := 0; i < 6; i++ {
		source += fmt.Sprintf("/* Returns the calculated value for this operation. */\nint example%d() {\n int i = 0;\n i += 1;\n i += 2;\n printf(\"%%d\", i);\n return i;\n}\n", i)
	}
	report, _ := engine.AnalyzeSubmissionStyle(writeStyleSubmission(t, t.TempDir(), "s", map[string]string{"lab.c": source}))
	if report.Score >= 0.5 || report.Flagged {
		t.Fatalf("correlated comment features alone must not flag: %+v", report)
	}
}

func TestTokenMetricsHoldOutSubmissionAndRequirePeers(t *testing.T) {
	root := t.TempDir()
	var subs []domain.Submission
	for i := 0; i < 5; i++ {
		source := studentStyled + "\nint extra(int value) { return value" + strings.Repeat(" + value", i+1) + "; }\n"
		subs = append(subs, writeStyleSubmission(t, root, fmt.Sprint(i), map[string]string{"lab.c": source}))
	}
	compute := func(subs []domain.Submission) domain.AIDetectionReport {
		t.Helper()
		r, err := engine.ComputeAIDetectionFromFingerprints(subs, engine.FingerprintSubmissions(subs, "lab.c", similarityConfig), "lab.c", nil, similarityConfig)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, sub := range compute(subs[:4]).Submissions {
		if sub.TokenMetrics == nil || sub.TokenMetrics.Perplexity != nil {
			t.Fatal("perplexity requires four eligible peers")
		}
	}
	base := compute(subs)
	for _, sub := range base.Submissions {
		m := sub.TokenMetrics
		if m == nil || m.Perplexity == nil || *m.Perplexity < 1 || m.CohortSize != 4 || math.Abs(math.Log2(*m.Perplexity)-*m.CrossEntropyBits) > 0.002 {
			t.Fatalf("invalid metrics: %+v", m)
		}
		if sub.Flagged {
			t.Fatal("token statistics cannot flag submissions")
		}
	}
	changed := strings.ReplaceAll(studentStyled+"\nint extra(int value) { return value + value; }\n", "escriu", "entirely_novel_name")
	subs[0] = writeStyleSubmission(t, root, "0", map[string]string{"lab.c": changed})
	var original, novel *domain.AITokenMetrics
	for _, sub := range base.Submissions {
		if sub.SubmissionID == "0" {
			original = sub.TokenMetrics
		}
	}
	for _, sub := range compute(subs).Submissions {
		if sub.SubmissionID == "0" {
			novel = sub.TokenMetrics
		}
	}
	if *novel.Perplexity <= *original.Perplexity {
		t.Fatal("held-out novel identifiers must be less predictable than identifiers present in the peers")
	}
	short := writeStyleSubmission(t, root, "short", map[string]string{"lab.c": "int main(void) { return 0; }"})
	if compute([]domain.Submission{short}).Submissions[0].TokenMetrics != nil {
		t.Fatal("short samples must abstain")
	}
}
