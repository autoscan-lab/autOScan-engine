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
  Two comment measurements count as one family. Formatting cannot independently
  raise a flag or alter the structural baseline.
- Each feature has a sample minimum. Whole sources rejected by the C parser
  supply no style or token metrics; their provenance tells can still be read.
  Missing/unreadable files are skipped. No readable parsed code means no style
  report. Cohort percentiles exclude submissions without measured features.
- Flagged similarity pairs can link to independently flagged submissions.
  Links do not change flags, chain suspicion, or establish common authorship.
- An absent/empty/unusable dictionary does not disable the other signals.
  Invalid dictionary entries remain visible in `dictionary_errors`.

The style score uses fixed feature ramps in `internal/engine/ai_style.go`:
`clamp((value - lower) / (upper - lower), 0, 1)`. The baseline uses the full
5.75-weight review budget, so missing features do not amplify the others.
Each strength is multiplied by `sqrt(min(1, samples / target))`; targets are
10 comments, 8 functions, 10 checkable calls, and 400 parsed tokens for idioms.
The raw feature `score` stays unchanged for comparisons across earlier labs;
`sample_count` and `reliability` describe the separate sample support.
The baseline and contributions use the supported strengths. These support
targets are explicit heuristic controls, not calibrated confidence intervals.
These anchors are subjective, not trained authorship boundaries. Correct error
handling and good documentation are legitimate programming practices.

The `defensive_idioms` key remains stable, but its label is course-specific style
markers. Its ramp is 0–14 capped marker occurrences per 100 effective lines,
where effective lines are `max(code_lines, ceil(parsed_tokens / 12))`.
At least 50 parsed tokens or 30 code lines are required; each marker kind contributes at most five
occurrences. It includes const declarations/parameters/returns, static function
headers, enum definitions, array parameters, function forward declarations,
explicit void parameter lists, unused-variable void casts, sizeof expressions
on visible variables, uppercase variable names, const void pointer parameters,
checked wait/waitpid results, empty wait loops, EXIT_SUCCESS/EXIT_FAILURE, and
numbered phase/step comments. Prior guard, buffer, and checked-call idioms
remain. Additional weak markers cover volatile/sig_atomic_t declarations as
one habit, bool/_Bool, GNU unused attributes, discarded call results, errno,
capitalized object/field names, literal /tmp paths, custom _t typedef names,
and POSIX semaphore APIs. pthread_t/key_t typedef names and the course's
semaphore/SEM_* wrappers are excluded. EXIT calls count as a signal-handler
marker only when signal/sigaction registration resolves to that function;
same-spelled action objects in different scopes are not linked. These are
review signals, not new banned constructs. A single style habit alone cannot
produce a style review flag.

C syntax nodes identify these shapes regardless of object names, types,
whitespace, or qualifier order. Uppercase macros and typedef names are not
uppercase variables; sizeof(type) is not sizeof(variable). Declarations and
operators inside comments or strings are not code evidence. Phase comments
are recognized separately in English/Spanish numbered phase/step forms.

## Local entropy and perplexity

`token_metrics` has no effect on flags. Its model is a local statistical C-token
model, not the perplexity of Claude, ChatGPT, or any pretrained language model.
It makes no network calls and persists no model or index.

`method` is `peer-bigram-v2`. Two token streams retain original identifiers or
replace them consistently with first-occurrence symbols. Literal contents are
normalized and comments/directives are excluded; C code inside conditional
preprocessor blocks is retained. Bigram context resets at file boundaries.
The GNU unused-parameter spelling unsupported by the parser is masked only
when a second parse accepts the complete source, preserving byte/line offsets.
Other syntax failures remain unavailable.

For at least 50 parsed tokens, `entropy_bits` is empirical unigram Shannon
entropy: `-sum(p(token) * log2(p(token)))`. Formatting and naming entropy are
separate normalized Shannon entropies over measured style choices.

Entropy requires 50 tokens. Peer likelihood requires 128 target tokens and at
least four distinct peer structures. Equivalent normalized token streams form
one group; the entire target group is held out and other groups contribute once.
This removes exact structural duplicates, including consistently renamed copies;
near-duplicates and common instructor functions are not automatically removed.
`cohort_size` counts distinct eligible peer structures, and `excluded_peers`
counts duplicate/equivalent peers excluded from that target's model.
Unseen tokens share one unknown bucket. Sparse contexts back off to smoothed
unigram probabilities, rather than treating all next tokens uniformly:

`U(token) = (peer_token_count + 0.5) / (peer_token_total + 0.5 * vocabulary_size)`

`P(token | previous) = (peer_bigram_count + 5 * U(token)) / (peer_context_count + 5)`

`cross_entropy_bits` is mean token surprisal `-log2(P)`; `perplexity` is
`2^cross_entropy`; `surprisal_stddev_bits` is the population standard deviation
of surprisal. Values are rounded to three decimals after calculation. Absent
peer fields mean insufficient cohort evidence, not zero. Common assignment
templates, copied code, vocabulary differences, and mixed authorship all affect
these numbers; low perplexity is not evidence of AI authorship by itself.
Normalized entropy/cross-entropy/perplexity are reported separately from the
original identifiers; `unknown_token_share` exposes lexical mismatch.
Mean cross-entropy is also mean negative log-likelihood (NLL) in bits, not an
independent signal or additional contribution.

Local profiles use consecutive 128-token windows within each file, merging a
tail shorter than 64 tokens. A file with fewer than 64 tokens has no window.
Each window carries file/line bounds, normalized token entropy, mean NLL,
perplexity and token surprisal deviation. `window_stddev_bits` measures the
deviation of window means, and `adjacent_window_change_bits` averages absolute
changes between neighboring windows within a file. `window_burstiness` is
`(stddev(window_means) - mean(window_means)) / (stddev + mean)`, bounded -1 to 1.
Variation needs two windows; adjacency never crosses file boundaries. At most
32 windows with the largest deviations are returned, while summaries and
`window_count` use all windows. These profiles are context, not classified AI
regions, and add no AI points.

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
Its `score` is on a 0–100 scale and its `method` is `contextual-v2`. This is
heuristic suspicion, not confidence or an authorship probability. Zero means
no measured indications; it does not certify human authorship.

Style supplies the weighted baseline above. Each measured review feature has
a named contribution in points, referencing the corresponding `feature_key`.
Rounding is reconciled so displayed contributions sum to the total. Dictionary
coverage retains its floor of `best_score * 100`. Code patterns no longer set
fixed 20/30/40-point floors. For n occurrences among o relevant constructs and
t parsed tokens, a rule's strength is:

`anchor * n/(n+2) * sqrt(n/(max(n,o)+5)) * sqrt(t/(t+100)) * sqrt(min(1,100*n/t)) * (0.5+0.5*f/3)`

Here f is the count of independently supported style families, capped at three.
Anchors retain the previous relative priorities: 40 initialized arrays, 30 VLAs
or wrapped calls, 20 complex conditions. Opportunities count arrays, all calls,
or control conditions respectively. The terms damp small samples, isolated
occurrences, low prevalence, and low token density. Both array rules share their
stronger combined strength; all four patterns share a 24-point budget and
remaining score headroom. Full occurrence counts survive capped examples.
The controls are transparent heuristics and have not been learned from labels.

Exact formatting profiles separate assignment, comparison, logical and
arithmetic operators, control/call parentheses, commas, and indentation.
Horizontal gaps distinguish exact space/tab counts from newlines and comments.
Profiles report dominant counts and entropy, with at least five observations.
Trivial trailing-space and blank-line habits do not dilute formatting entropy.
A separate formatting contribution is at most four points, needs two supported
style families and four formatting contexts with at least 20 observations each,
and starts only above 95% mean consistency. Sample support and remaining
headroom reduce it. Formatting alone contributes zero; the style baseline and
flags are invariant to formatting. Naming entropy remains context only.

Tool configuration supplies a context-weighted floor of at most 10 points,
scaled by parsed token support and independent style families, with no
duplicate-file bonuses. Without parsed source support it supplies no score.
Explicit generator attribution/model self-identification retains its 90-point
floor. These are review priorities, not measured detection rates. Contributions
are rounded and reconciled to sum exactly to the one-decimal total.

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
`contextual-v2+history-v1` for new runs. Raw shared feature strengths avoid a
history bonus caused solely by changing sample support. History never changes
the saved engine score or grades. Token entropy, peer perplexity, cohort rank, generic chat wording, typography,
and similarity links add no points.

## Research boundaries

NLL, perplexity, surprisal variation and entropy profiles describe the same
underlying local token distributions; they are not independent votes for AI.
DetectGPT/DetectCodeGPT curvature requires likelihoods from a suitable pretrained
model and controlled perturbations. Our token model discards whitespace, so
whitespace perturbations would be invariant and cannot implement that method.
No pretrained scorer, provider, model download or remote student upload is added.
See [DetectGPT](https://arxiv.org/abs/2301.11305),
[DetectCodeGPT](https://arxiv.org/abs/2401.06461), and the
[perplexity comparison](https://arxiv.org/abs/2412.16525).
AST subtree/transition models need trustworthy human/AI reference corpora before
they can produce class likelihood ratios. CFGs/PDGs can contain cycles; claims
that AI code has universal complexity or spectral limits are not used as rules.
Synthetic invariance tests and unlabeled cohort runs cannot establish accuracy.

## Validation limits

Synthetic tests verify evidence boundaries, sample minima, parser failures,
result-use checks, multiple files, no flag propagation, and held-out token
metrics. They verify implementation behavior, not detection accuracy.

A local smoke run on October 2, 2026 read eight supplied S0-S3 ZIPs and processed
267 discovered C submissions without executing student code or uploading it.
With contextual-v2, 245 supplied scores and local peer-token profiles; 22
remained unscored. Of the 267 submissions, 121 suggested review. All measured
contribution sums and source ranges passed validation. An inspected S3 pair
highlighted a shared random-array selection despite renamed bounds and a
const-qualified variable; overall similarity was 8.42%, below the configured
review threshold. Matching was tested without the production dictionary. One
nested TAR and one RAR had no directly discoverable C source and were outside
this smoke run. These results replace the earlier, smaller style rubric.

The contextual-v2 deployment now does this automatically: every saved run is
checked against `ai_detection.method`, refreshed from its embedded source
files, backed up and conditionally updated. Existing dictionary matches and
archive artifacts are preserved because the original upload may be deleted.
Only AI analysis changes; grades and tests remain intact. See
[automatic AI reanalysis](CLOUD_SETUP.md#automatic-ai-reanalysis) for progress,
retry behavior and customized storage prefixes.

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
