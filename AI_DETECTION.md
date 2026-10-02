# AI detection evidence

The detector supplies evidence for a grader to review. `flagged` means review
is suggested. It does not establish AI authorship and does not change grades.
Style thresholds are heuristic, with no measured false-positive rate. The
existing dictionary remains responsible for out-of-syllabus patterns.

## Signals and abstention

- Dictionary coverage keeps its existing thresholds: at least 50% of an entry
  must match, and 25% submission coverage raises a review flag.
- Every discovered `.c` file supplies source evidence. Tool artifacts are
  inspected in the submission directory and its own owner ancestors, never
  another student's directory. A tool configuration artifact raises a review
  flag but demonstrates configuration presence only, not generated code.
- Explicit model self-identification or generator attribution in comments raises
  a review flag. Generic
  assistant wording, Markdown fences, punctuation, emoji, and invisible
  characters remain context only. Ordinary multilingual text is not flagged.
- Style measures prose comments, documented functions, checked call results,
  course-specific style markers, and formatting/naming entropy. A review flag needs a score
  of at least 0.6, at least half the available review-feature weight, and strong
  support (at least 0.5) in two of three families: comments, call checks, idioms.
  Two comment measurements count as one family. Consistent formatting never
  raises the score. Formatting and naming variation cannot suppress it.
- Each feature has a sample minimum. Whole sources rejected by the C parser
  supply no style or token metrics; their provenance tells can still be read.
  Missing/unreadable files are skipped. No readable parsed code means no style
  report. Cohort percentiles exclude submissions without measured features.
- Flagged similarity pairs can link to independently flagged submissions.
  Links do not change flags, chain suspicion, or establish common authorship.
- An absent/empty/unusable dictionary does not disable the other signals.
  Invalid dictionary entries remain visible in `dictionary_errors`.

The style score uses fixed feature ramps in `internal/engine/ai_style.go`:
`clamp((value - lower) / (upper - lower), 0, 1)`. Review strengths are averaged
by their weights over measured review features. Entropy is context only.
These anchors are subjective, not trained authorship boundaries. Correct error
handling and good documentation are legitimate programming practices.

The `defensive_idioms` key remains stable, but its label is course-specific style
markers. Its ramp is 0–14 capped marker occurrences per 100 code lines, measured
only with at least 30 code lines; each marker kind contributes at most five
occurrences. It includes const declarations/parameters/returns, static function
headers, enum definitions, array parameters, function forward declarations,
explicit void parameter lists, unused-variable void casts, sizeof expressions
on visible variables, uppercase variable names, const void pointer parameters,
checked wait/waitpid results, empty wait loops, EXIT_SUCCESS/EXIT_FAILURE, and
numbered phase/step comments. Prior guard, buffer, and checked-call idioms
remain. A single style habit alone cannot produce a style review flag.

C syntax nodes identify these shapes regardless of object names, types,
whitespace, or qualifier order. Uppercase macros and typedef names are not
uppercase variables; sizeof(type) is not sizeof(variable). Declarations and
operators inside comments or strings are not code evidence. Phase comments
are recognized separately in English/Spanish numbered phase/step forms.

## Local entropy and perplexity

`token_metrics` has no effect on flags. Its model is a local statistical C-token
model, not the perplexity of Claude, ChatGPT, or any pretrained language model.
It makes no network calls and persists no model or index.

The tokenizer keeps identifier spelling and punctuation, excludes comments,
and replaces string, character, numeric, and include-path literals with their
node types. Bigram context resets at each file boundary.

For at least 50 parsed tokens, `entropy_bits` is empirical unigram Shannon
entropy: `-sum(p(token) * log2(p(token)))`. Formatting and naming entropy are
separate normalized Shannon entropies over measured style choices.

Peer metrics require five eligible submissions, each with at least 50 tokens.
The whole target submission is held out. Only the other submissions contribute
counts and vocabulary. Unseen tokens share one unknown bucket. Bigram
probabilities use additive smoothing with alpha 0.5:

`P(token | previous) = (peer_bigram_count + 0.5) / (peer_context_count + 0.5 * vocabulary_size)`

`cross_entropy_bits` is mean token surprisal `-log2(P)`; `perplexity` is
`2^cross_entropy`; `surprisal_stddev_bits` is the population standard deviation
of surprisal. Values are rounded to three decimals after calculation. Absent
peer fields mean insufficient cohort evidence, not zero. Common assignment
templates, copied code, vocabulary differences, and mixed authorship all affect
these numbers; low perplexity is not evidence of AI authorship by itself.

## Contract and rollout

New submission fields are additive: `ai_score`, `style`, `tells`, `token_metrics`, and
`similar_to_flagged`. `best_score` retains its dictionary-coverage meaning.
Tell `flagged` identifies review evidence versus context. Style feature
`evidence` is `review` or `context`; `value` is the raw measurement and `score`
its ramp strength. `style.cohort_size` counts measurable peers.

Grade analysis and sandbox analysis expose the same signals. A dictionary
minimum-score response filter retains independently flagged submissions even
when their dictionary coverage is zero. Deploy the engine first, then the app;
older app readers ignore these fields and the new app accepts old run JSON.

## Overall score and source locations

`ai_score` is absent when no review feature, usable dictionary analysis, or
flagged source evidence was measured. Missing evidence is not a zero score.
Its `score` is on a 0–100 scale and its `method` is `heuristic-v1`. This is
heuristic suspicion, not confidence or an authorship probability. Zero means
no measured indications; it does not certify human authorship.

Style supplies the weighted baseline above. Each measured review feature has
a named contribution in points, referencing the corresponding `feature_key`.
Rounding is reconciled so displayed contributions sum to the total. Dictionary
coverage sets a floor of `best_score * 100`. Requested code-pattern rules set
floors of 20 for complex multiline conditions, 30 for long wrapped calls or
variable-length arrays, and 40 for literal-initialized arrays.
Tool configuration sets a floor of 40; explicit generator attribution or model
self-identification sets a floor of 90. These floors are subjective review
priorities, not measured detection rates. In this order, each floor contributes
only the increase above the preceding total. Duplicate occurrences do not add
bonuses, and alternative signals are not summed as independent probabilities.
Style/dictionary evidence can reach 100 without establishing authorship.

A complex multiline condition is the condition of an `if` spanning at least
three source lines and containing at least three `&&`/`||` operators. A long
wrapped call has a multiline argument list with at least two arguments and
at least 100 characters when whitespace in the complete call is collapsed.
Comments are omitted when measuring the folded call length.
The function name does not affect this rule: `asprintf` is permitted and its
presence alone does not trigger review. Text in comments/strings and macro
definitions is excluded. Both patterns are explicit review flags, applied
only to sources accepted by the C parser. Short wrapped calls do not qualify.

Review style features expose `locations` and `location_count`. At most 64
examples per feature are returned, without reducing the measurement counts.
Each location names a submission-relative file, a one-based inclusive line
range, a label, and a bounded snippet. Style contributions reference these
locations instead of duplicating source payloads. Dictionary and provenance
contributions carry their own locations. A zero line on a tool artifact means
the file is evidence but cannot be linked into a C source panel. Tells may
include `end_line` so compound conditions can be highlighted completely.

The frontend optionally adds a separate, disclosed 15-point history contribution
for a qualifying two-lab/two-family style jump, capped at 100. Its method is
`heuristic-v1+history-v1`. History never changes the saved engine score or
grades. Entropy, peer perplexity, cohort rank, generic chat wording, typography,
and similarity links add no points.

## Validation limits

Synthetic tests verify evidence boundaries, sample minima, parser failures,
result-use checks, multiple files, no flag propagation, and held-out token
metrics. They verify implementation behavior, not detection accuracy.

A local smoke run on October 2, 2026 read eight supplied S0-S3 ZIPs and processed
267 discovered C submissions without executing student code or uploading it.
238 supplied parsed style/peer-token metrics and overall scores. With the
expanded course rules, 119 submissions suggested review. An inspected S3 pair
highlighted a shared random-array selection despite renamed bounds and a
const-qualified variable; overall similarity was 8.42%, below the configured
review threshold. Matching was tested without the production dictionary. One
nested TAR and one RAR had no directly discoverable C source and were outside
this smoke run. These results replace the earlier, smaller style rubric; old
saved results must be rerun to receive the new score and evidence.

These submissions have unknown authorship. Counts cannot estimate precision,
recall, false positives, or missed AI use. Scores and thresholds must be
evaluated on independently verified human and AI submissions from several labs
and models, with a held-out evaluation set, before claiming detection accuracy.
Clean human code can look generated; edited generated code can look informal.

## Independent banned-code policy

The scanner also reports course violations, independently of AI suspicion:
variable-length arrays, literal-initialized arrays (numeric/string lists,
including string-literal array initializers), and pthread attributes
(`pthread_attr_t` and `pthread_attr_*` calls). Violations use the existing
`banned_hits` fields with construct labels, file, line, column, and snippet.
They follow the existing banned-code grading path; an AI score never changes
a grade. VLA detection recognizes visible runtime object/parameter bounds and
function-call bounds, including runtime-sized typedef arrays. Fixed numeric,
known macro, enum, and sizeof(type) bounds stay allowed. Array parameters are
style markers, not VLA violations. Unknown names and function-like macros
whose expansion cannot be resolved abstain; macro expansion, header aliases,
and invalid sources are outside declaration detection. No student program is
executed for this scan.

The global `banned.yaml` remains compatible with its existing `banned` list:

```yaml
banned: [printf]
constructs:
  variable_length_arrays: true
  initialized_arrays: true
  pthread_attributes: true
```

All three construct rules default to enabled, including when an old document
omits them. Set each field to false to disable that grading ban. The frontend
Banned code panel edits them and preserves existing function bans. This does
not disable the separate course-specific AI review markers. Existing forbidden
function calls still follow the function list; asprintf has no new ban.
No live policy or deployment is changed by this implementation.
