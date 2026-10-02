package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/internal/engine"
	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

var similarityConfig = domain.CompareConfig{MinMatchTokens: 12, MinFuncTokens: 20, ScoreThreshold: 0.7}

const similarityOriginal = `#include <stdio.h>
#include <unistd.h>
#include <sys/wait.h>

int sum_range(int *values, int start, int end) {
    int total = 0;
    for (int i = start; i < end; i++) {
        if (values[i] > 0) {
            total += values[i];
        }
    }
    return total;
}

int main(void) {
    int values[8] = {1, 2, 3, 4, 5, 6, 7, 8};
    int fds[2];
    if (pipe(fds) < 0) {
        perror("pipe");
        return 1;
    }
    pid_t pid = fork();
    if (pid == 0) {
        close(fds[0]);
        int partial = sum_range(values, 0, 4);
        write(fds[1], &partial, sizeof(partial));
        close(fds[1]);
        return 0;
    }
    close(fds[1]);
    int child_total = 0;
    read(fds[0], &child_total, sizeof(child_total));
    close(fds[0]);
    waitpid(pid, NULL, 0);
    printf("Total: %d\n", child_total + sum_range(values, 4, 8));
    return 0;
}
`

// Same program with renamed identifiers, reworded messages and comments.
const similarityRenamed = `#include <stdio.h>
#include <unistd.h>
#include <sys/wait.h>

/* adds the positive numbers of a slice */
int sumar(int *nums, int ini, int fin) {
    int acc = 0;
    for (int k = ini; k < fin; k++) {
        if (nums[k] > 0) {
            acc += nums[k];
        }
    }
    return acc;
}

int main(void) {
    int nums[8] = {1, 2, 3, 4, 5, 6, 7, 8};
    int tub[2];
    if (pipe(tub) < 0) {
        perror("Error creando la tuberia");
        return 1;
    }
    pid_t hijo = fork();
    if (hijo == 0) {
        close(tub[0]);
        int parcial = sumar(nums, 0, 4);
        write(tub[1], &parcial, sizeof(parcial));
        close(tub[1]);
        return 0;
    }
    close(tub[1]);
    int suma_hijo = 0;
    read(tub[0], &suma_hijo, sizeof(suma_hijo));
    close(tub[0]);
    waitpid(hijo, NULL, 0);
    printf("La suma total es %d\n", suma_hijo + sumar(nums, 4, 8));
    return 0;
}
`

const similarityIndependent = `#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    char name[32];
    float grade;
} Student;

static int compare_grades(const void *a, const void *b) {
    const Student *x = a;
    const Student *y = b;
    if (x->grade < y->grade) return 1;
    if (x->grade > y->grade) return -1;
    return strcmp(x->name, y->name);
}

int main(int argc, char **argv) {
    Student list[4] = {{"ana", 7.5f}, {"bob", 9.0f}, {"cai", 6.0f}, {"dan", 9.0f}};
    qsort(list, 4, sizeof(Student), compare_grades);
    for (int i = 0; i < 4 && i < argc + 3; i++) {
        printf("%s %.1f\n", list[i].name, list[i].grade);
    }
    return EXIT_SUCCESS;
}
`

func writeSubmissions(t *testing.T, sources map[string]string) []domain.Submission {
	t.Helper()
	root := t.TempDir()
	var subs []domain.Submission
	for id, src := range sources {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "lab.c"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		subs = append(subs, domain.Submission{ID: id, Path: dir})
	}
	return subs
}

func similarityScores(t *testing.T, sources map[string]string) map[[2]string]domain.SimilarityPairResult {
	t.Helper()
	subs := writeSubmissions(t, sources)
	prints := engine.FingerprintSubmissions(subs, "lab.c", similarityConfig)
	report, err := engine.ComputeSimilarityFromFingerprints(subs, prints, "lab.c", similarityConfig)
	if err != nil {
		t.Fatal(err)
	}
	out := map[[2]string]domain.SimilarityPairResult{}
	for _, pair := range report.Pairs {
		out[[2]string{pair.A, pair.B}] = pair
		out[[2]string{pair.B, pair.A}] = pair
	}
	return out
}

func TestSimilarityRanksRenamedCopyAboveIndependentWork(t *testing.T) {
	scores := similarityScores(t, map[string]string{
		"original":    similarityOriginal,
		"renamed":     similarityRenamed,
		"independent": similarityIndependent,
	})

	copied := scores[[2]string{"original", "renamed"}]
	if copied.SimilarityPercent < 99 || !copied.Flagged {
		t.Fatalf("renamed copy scored %.1f%% (flagged=%v), want ~100%%", copied.SimilarityPercent, copied.Flagged)
	}
	if independent := scores[[2]string{"original", "independent"}]; independent.SimilarityPercent > 30 {
		t.Fatalf("independent pair scored %.1f%%, want < 30%%", independent.SimilarityPercent)
	}

	if len(copied.Matches) == 0 {
		t.Fatal("expected matched regions for the copied pair")
	}
	for _, match := range copied.Matches {
		if len(match.SpansA) != 1 || len(match.SpansB) != 1 {
			t.Fatalf("each tile should map to one span per side, got %d/%d", len(match.SpansA), len(match.SpansB))
		}
		if match.SpansA[0].StartLine < 5 || match.SpansB[0].StartLine < 5 {
			t.Fatalf("tile starts outside the function bodies: %+v", match)
		}
	}
}

func TestSimilarityIgnoresRepeatedBoilerplate(t *testing.T) {
	chain := func(name string) string {
		src := "const char *" + name + "(int v) {\n"
		for i := 0; i < 12; i++ {
			src += "    if (v == 1) { return \"x\"; }\n"
		}
		return src + "    return \"y\";\n}\n"
	}
	scores := similarityScores(t, map[string]string{
		"short": chain("pick") + similarityIndependent,
		"long":  chain("pick") + chain("other") + chain("third") + similarityOriginal,
	})

	// Each token matches at most once, so the long file's extra chains can't inflate the score.
	if pair := scores[[2]string{"short", "long"}]; pair.SimilarityPercent > 45 {
		t.Fatalf("repeated chains scored %.1f%%, want them counted once", pair.SimilarityPercent)
	}
}

const aiSumEntry = `int add_positive(int *xs, int from, int to) {
    int s = 0;
    for (int j = from; j < to; j++) {
        if (xs[j] > 0) {
            s += xs[j];
        }
    }
    return s;
}`

func aiDetect(t *testing.T, dict *aipkg.Dictionary, sources map[string]string) map[string]domain.AISubmissionResult {
	t.Helper()
	subs := writeSubmissions(t, sources)
	prints := engine.FingerprintSubmissions(subs, "lab.c", similarityConfig)
	report, err := engine.ComputeAIDetectionFromFingerprints(subs, prints, "lab.c", dict, similarityConfig)
	if err != nil {
		t.Fatal(err)
	}
	if report.DictionaryUsable != len(dict.Entries) || len(report.DictionaryErrors) != 0 {
		t.Fatalf("short entries should be usable, got usable=%d errors=%+v", report.DictionaryUsable, report.DictionaryErrors)
	}
	out := map[string]domain.AISubmissionResult{}
	for _, sub := range report.Submissions {
		out[sub.SubmissionID] = sub
	}
	return out
}

func TestAIDetectionScoresShareOfCodeMadeOfPatterns(t *testing.T) {
	dict := &aipkg.Dictionary{Entries: []aipkg.Entry{
		{ID: "sum", Title: "Positive sum", Code: aiSumEntry},
		{ID: "swap16", Title: "Manual swap", Code: `unsigned short swap16(unsigned short x) {
    return (unsigned short)((x << 8) | (x >> 8));
}`},
	}}
	results := aiDetect(t, dict, map[string]string{
		"embedded": similarityOriginal,
		"only":     aiSumEntry,
	})

	// The sum helper is a minority of this program, so the score is its share of the code.
	embedded := results["embedded"]
	if embedded.Score < 0.15 || embedded.Score > 0.5 {
		t.Fatalf("embedded pattern scored %.2f, want its share of the file", embedded.Score)
	}
	if len(embedded.Matches) != 1 || embedded.Matches[0].EntryID != "sum" || embedded.Matches[0].Score < 0.99 || len(embedded.Matches[0].Spans) == 0 {
		t.Fatalf("want one full match of the sum pattern, got %+v", embedded.Matches)
	}

	if only := results["only"]; only.Score < 0.99 || !only.Flagged {
		t.Fatalf("a file that is the pattern scored %.2f (flagged=%v), want ~1", only.Score, only.Flagged)
	}
}

func TestAIDetectionIgnoresPatternFragments(t *testing.T) {
	// Only the sum helper of this two-function pattern appears, under half of it.
	dict := &aipkg.Dictionary{Entries: []aipkg.Entry{{ID: "pair", Title: "Sum and report", Code: aiSumEntry + `

void report_totals(int *xs, int n, int threshold) {
    int above = 0;
    int below = 0;
    for (int k = 0; k < n; k++) {
        if (xs[k] > threshold) {
            above++;
        } else {
            below++;
        }
    }
    printf("above=%d below=%d\n", above, below);
    if (above > below) {
        printf("mostly above\n");
    }
}`}}}
	results := aiDetect(t, dict, map[string]string{"embedded": similarityOriginal})
	if got := results["embedded"]; got.Score != 0 || len(got.Matches) != 0 {
		t.Fatalf("a fragment of a pattern scored %.2f with %d matches, want 0", got.Score, len(got.Matches))
	}
}

// Moving declarations or adding a statement first used to renumber every later name.
func TestSimilaritySurvivesMovedDeclarations(t *testing.T) {
	inline := `int happens(int p);
void print_line(const char *s);
int pick_level(int parent);
int pick_rarity(void);
void announce(int level, int rarity);
int launch(int level, int count);

int explore(int parent) {
    srand(getpid());
    if (happens(5)) {
        print_line("ruined");
        return 0;
    }
    int level = pick_level(parent);
    int rarity = pick_rarity();
    announce(level, rarity);
    if (level == 3 && happens(1)) {
        print_line("life");
    }
    if (level == 3 || level == 4) {
        return 0;
    }
    return launch(level, rarity + 1);
}
`
	hoisted := `int ocurre(int p);
void escribir(const char *s);
int elegir_nivel(int padre);
int elegir_rareza(void);
void anunciar(int nivel, int rareza);
int lanzar(int nivel, int cuantos);

int explorar(int padre) {
    int nivel;
    int rareza;
    int hijos;
    if (ocurre(5)) {
        escribir("arruinado");
        return 0;
    }
    nivel = elegir_nivel(padre);
    rareza = elegir_rareza();
    anunciar(nivel, rareza);
    if (nivel == 3 && ocurre(1)) {
        escribir("vida");
    }
    if (nivel == 3 || nivel == 4) {
        return 0;
    }
    hijos = rareza + 1;
    return lanzar(nivel, hijos);
}
`
	scores := similarityScores(t, map[string]string{"inline": inline, "hoisted": hoisted})
	if pair := scores[[2]string{"inline", "hoisted"}]; pair.SimilarityPercent < 40 {
		t.Fatalf("reordered declarations scored %.1f%%, want >= 40%%", pair.SimilarityPercent)
	}
}

// Without markers a function header plus declarations matched a run of plain declarations.
func TestSimilarityDoesNotMatchHeadersAgainstDeclarations(t *testing.T) {
	header := `int launch(int level, int total) {
    int started = 0;
    int result = 0;
    int status;
    return started + result + status + level * total;
}
`
	declarations := `int explore(void) {
    int level;
    int rarity;
    int total;
    int launched = 0;
    int failed = 0;
    int status;
    while (launched < 3) {
        launched++;
    }
    return failed;
}
`
	scores := similarityScores(t, map[string]string{"header": header, "declarations": declarations})
	if pair := scores[[2]string{"header", "declarations"}]; len(pair.Matches) != 0 {
		t.Fatalf("header matched declarations: %.1f%% with %d tiles", pair.SimilarityPercent, len(pair.Matches))
	}
}
