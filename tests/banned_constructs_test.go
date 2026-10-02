package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

func TestBannedConstructsUseSyntaxAcrossNamesAndTypes(t *testing.T) {
	for _, example := range []struct{ source, label string }{
		{"int f(int count) { char bytes[count]; return 0; }", "Variable-length array"},
		{"typedef unsigned long Item; int f(int renamed) { Item data[renamed + 2]; return 0; }", "Variable-length array"},
		{"int f() { int width = 4; double grid[2][width]; return 0; }", "Variable-length array"},
		{"int capacity(); int f() { char values[capacity()]; return 0; }", "Variable-length array"},
		{"int f(int count) { typedef long Row[count]; Row data; return 0; }", "Variable-length array"},
		{"const char *objects[] = {\"First\", \"Second\"};", "Initialized array"},
		{"int rows[] = {-1, 0, 1}, cols[] = {1, 0, -1};", "Initialized array"},
		{"typedef long Item; Item renamed[2] = {1, 2};", "Initialized array"},
		{"char greeting[] = \"hello\";", "Initialized array"},
		{"int f() { pthread_attr_t settings; return 0; }", "pthread_attr_t"},
		{"int f() { int result = pthread_attr_init(&renamed); return result; }", "pthread_attr_init"},
		{"int f() { pthread_attr_setdetachstate( &settings, PTHREAD_CREATE_JOINABLE ); return 0; }", "pthread_attr_setdetachstate"},
	} {
		t.Run(example.label+example.source, func(t *testing.T) {
			sub := writeStyleSubmission(t, t.TempDir(), "example", map[string]string{"lab.c": example.source})
			result := engine.NewScanEngine(&policy.Policy{}).ScanAll([]domain.Submission{sub})[0]
			if len(result.HitsByFunction[example.label]) == 0 {
				t.Fatalf("missing %s: %+v", example.label, result)
			}
			for _, hit := range result.Hits {
				if hit.File != "lab.c" || hit.Line < 1 || hit.Snippet == "" {
					t.Fatalf("bad location: %+v", hit)
				}
			}
		})
	}
	for _, source := range []string{
		"#define LIMIT 4\nint f() { char bytes[LIMIT]; return 0; }",
		"enum { LIMIT = 4 }; int f() { char bytes[LIMIT]; return 0; }",
		"typedef int Number; int f() { char bytes[sizeof(Number)]; return 0; }",
		"int f() { char fixed[128]; char *dynamic = malloc(128); asprintf(&dynamic, \"%d\", 2); return 0; }",
		"int f(char text[]) { return 0; }",
		"#define CAPACITY(x) 8\nint f() { char fixed[CAPACITY(2)]; return 0; }",
		"int f() { /* int forbidden[variable]; pthread_attr_t */ char *text = \"pthread_attr_init(&attr)\"; return 0; }",
	} {
		sub := writeStyleSubmission(t, t.TempDir(), "allowed", map[string]string{"lab.c": source})
		result := engine.NewScanEngine(&policy.Policy{}).ScanAll([]domain.Submission{sub})[0]
		if len(result.Hits) != 0 {
			t.Fatalf("allowed syntax banned: %s %+v", source, result)
		}
	}
}

func TestGlobalConstructOverridesPreserveBannedFunctions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "banned.yaml")
	if err := os.WriteFile(path, []byte("banned: [printf]\nconstructs:\n  variable_length_arrays: false\n  initialized_arrays: false\n  pthread_attributes: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := policy.LoadGlobalBannedPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	p := &policy.Policy{BannedFunctions: config.Banned, BannedConstructs: config.Constructs}
	sub := writeStyleSubmission(t, t.TempDir(), "example", map[string]string{"lab.c": "int f(int n) { char bytes[n]; int rows[] = {1,2}; pthread_attr_t attr; pthread_attr_init(&attr); printf(\"hi\"); return 0; }"})
	result := engine.NewScanEngine(p).ScanAll([]domain.Submission{sub})[0]
	if len(result.Hits) != 1 || result.Hits[0].Function != "printf" {
		t.Fatalf("overrides must preserve function bans: %+v", result)
	}
	missing, err := policy.LoadGlobalBannedPolicy(path + ".missing")
	if err != nil || !missing.Constructs.BanVariableLengthArrays() || !missing.Constructs.BanInitializedArrays() || !missing.Constructs.BanPthreadAttributes() {
		t.Fatalf("missing globals should retain defaults: %+v %v", missing, err)
	}
}

func TestCourseStyleMarkersRetainLocationsAcrossForms(t *testing.T) {
	source := `#define UPPER_MACRO 2
typedef long Number;
const char *format(Number choice);
void explore(Number step);
int main(void) {
 const int UPPER_VARIABLE = 3;
 Number input = 4;
 char text[128];
 (void) input;
 enum Mode { FIRST, SECOND };
 while ( wait (0) > 0 );
 while (waitpid (1, 0, 0) > 0);
 int status = waitpid(1, 0, 0);
 if (status < 0) return EXIT_FAILURE;
 int amount = sizeof(text);
 int type_size = sizeof(Number);
 int ordinary = sizeof(int);
 return EXIT_SUCCESS;
}
const char *format(Number choice) { return "ready"; }
const static int helper(const char text[], const void *left) { return 0; }
// Fase 1: escáner
int phase() { return 0; }
`
	source += strings.Repeat("\nint extra_line;", 20)
	result := scoredSubmission(t, map[string]string{"lab.c": source})
	feature := styleFeature(t, result.Style, "defensive_idioms")
	for _, label := range []string{"const declarations", "uppercase variable names", "unused-variable (void) casts", "enum definitions", "empty wait-result loops", "checked wait/waitpid results", "sizeof variable expressions", "explicit void parameter lists", "function forward declarations", "const-qualified function returns", "static functions", "array parameters", "const void pointer parameters", "Numbered solution-phase comment", "EXIT_SUCCESS/EXIT_FAILURE"} {
		found := false
		for _, location := range feature.Locations {
			if location.Label == label {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing marker %q: %s %+v", label, feature.Detail, feature.Locations)
		}
	}
	counts := map[string]int{}
	for _, location := range feature.Locations {
		counts[location.Label]++
	}
	if counts["uppercase variable names"] != 1 || counts["sizeof variable expressions"] != 1 || counts["empty wait-result loops"] != 2 {
		t.Fatalf("types/macros must not masquerade as variables: %+v", counts)
	}
}
