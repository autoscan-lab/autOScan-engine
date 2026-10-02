package tests

import (
	"strings"
	"testing"
)

func TestAdditionalCourseMarkersUseSyntaxAndExactLocations(t *testing.T) {
	source := `#include <signal.h>
#include <stdbool.h>
#include <errno.h>
#include <semaphore.h>
#define QUEUE_PATH "/tmp"
#define QUEUE_ID 'Q'
typedef struct { long Type; int Player; int Op; char Text[128]; } Msg;
typedef struct { double value; int finished; } shared_data_t;
static volatile sig_atomic_t stop = 0;
sem_t lock;
void unrelated(int number) { exit(0); }
void cleanup(int signum __attribute__((unused))) {
  (void)signum;
  (void)kill(42, SIGTERM);
  bool finished = true;
  if (errno == EINTR) { strerror(errno); }
  exit(0);
}
int main() {
  signal(SIGINT, cleanup);
  sem_init(&lock, 0, 1);
  sem_wait(&lock);
  sem_post(&lock);
  return 0;
}`
	result := scoredSubmission(t, map[string]string{"lab.c": source})
	feature := styleFeature(t, result.Style, "defensive_idioms")
	for _, label := range []string{"literal /tmp paths", "capitalized variable or field names", "custom _t typedef names", "volatile/sig_atomic_t declarations", "POSIX semaphores", "unused attributes", "unused-variable (void) casts", "discarded call results (void)", "boolean types", "errno access", "errno retries", "strerror", "exit from registered signal handlers"} {
		found := false
		for _, location := range feature.Locations {
			if location.Label == label {
				found = true
				if location.File != "lab.c" || location.StartLine < 1 || location.EndLine < location.StartLine || location.EndLine > len(strings.Split(source, "\n")) {
					t.Fatalf("invalid %s location %+v", label, location)
				}
				if label == "exit from registered signal handlers" && location.StartLine != 17 {
					t.Fatalf("ordinary exit was mistaken for handler exit %+v", location)
				}
			}
		}
		if !found {
			t.Errorf("marker %s missing: %s", label, feature.Detail)
		}
	}
	assertContributionTotal(t, result)
}

func TestSignalHandlerDetectionSupportsSigactionAndRenamedHandlers(t *testing.T) {
	source := `#include <signal.h>
void close_client(int code) { (void)code; _exit(0); }
int main() {
  struct sigaction action;
  action.sa_handler = &close_client;
  sigaction(SIGINT, &action, 0);
  exit(0);
  int count = 0; count += 1;
  return 0;
}`
	result := scoredSubmission(t, map[string]string{"lab.c": source})
	count := 0
	for _, feature := range result.Style.Features {
		for _, location := range feature.Locations {
			if location.Label == "exit from registered signal handlers" {
				count++
				if location.StartLine != 2 {
					t.Fatal("incorrect registered handler")
				}
			}
		}
	}
	if count != 1 {
		t.Fatalf("sigaction registered handler missing or duplicated: %+v", result.Style)
	}
}

func TestCourseWrappersAndStandardTypesAreExcluded(t *testing.T) {
	source := `#include <pthread.h>
#define QUEUE_ID 'Q'
#define OTHER_PATH "/tmpnotatempdirectory"
#define BOOL_EXAMPLE bool
// volatile sig_atomic_t stop; sem_init(&lock,0,1); errno; __attribute__((unused));
typedef int pthread_t;
typedef int key_t;
typedef struct { long type; int player; char text[128]; } Msg;
semaphore sem_mutex;
semaphore sem_in_h;
void handleSigint(int sig) { printF("errno bool volatile /tmp sem_init"); exit(0); }
int main() {
  pthread_t thread;
  key_t key;
  SEM_constructor_with_name(&sem_mutex, 2);
  SEM_constructor_with_name(&sem_in_h, 5);
  SEM_init(&sem_mutex, 1);
  SEM_init(&sem_in_h, 0);
  return 0;
}`
	result := scoredSubmission(t, map[string]string{"lab.c": source})
	for _, feature := range result.Style.Features {
		for _, location := range feature.Locations {
			switch location.Label {
			case "literal /tmp paths", "capitalized variable or field names", "custom _t typedef names", "POSIX semaphores", "boolean types", "volatile/sig_atomic_t declarations", "errno access", "unused attributes", "exit from registered signal handlers":
				t.Fatalf("ordinary wrappers, types or text triggered marker %+v", location)
			}
		}
	}
}

func TestVolatileAndAtomicTypesShareOneMarkerPerDeclaration(t *testing.T) {
	source := "volatile sig_atomic_t stopped = 0;\nvolatile int ready = 0;\nsig_atomic_t done = 0;\nint run(int value) { " + strings.Repeat("value += 1;", 10) + "return value; }\n"
	result := scoredSubmission(t, map[string]string{"lab.c": source})
	count := 0
	for _, feature := range result.Style.Features {
		for _, location := range feature.Locations {
			if location.Label == "volatile/sig_atomic_t declarations" {
				count++
			}
		}
	}
	if count != 3 {
		t.Fatalf("correlated qualifiers counted separately: %d", count)
	}
}

func TestHandlerRegistrationDoesNotCrossVariableScopes(t *testing.T) {
	source := `void finish(int n) { exit(0); }
void setup() { struct sigaction action; action.sa_handler = finish; }
int run(int n) {
 struct sigaction action;
 sigaction(2, &action, 0);
 n += 1; n += 2; n += 3; n += 4;
 return n;
}`
	result := scoredSubmission(t, map[string]string{"lab.c": source})
	for _, feature := range result.Style.Features {
		for _, location := range feature.Locations {
			if location.Label == "exit from registered signal handlers" {
				t.Fatal("unrelated action objects were linked by spelling")
			}
		}
	}
}

func TestUnusedAttributeCompatibilityKeepsBrokenSourcesUnavailable(t *testing.T) {
	for _, source := range []string{
		"void handler(int sig __attribute__((unused))) { invalid( ; }",
		"// __attribute__((unused))\nint main( {",
		"#define IGNORE __attribute__((unused))\nint main( {",
	} {
		result := scoredSubmission(t, map[string]string{"lab.c": source})
		if result.Style != nil || result.TokenMetrics != nil || result.AIScore != nil {
			t.Fatalf("unrelated syntax was accepted: %+v", result)
		}
	}
}
