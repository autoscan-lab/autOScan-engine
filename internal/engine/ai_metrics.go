package engine

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	sitter "github.com/smacker/go-tree-sitter"
)

const minMetricTokens = 50
const minPeerTokens = 128
const minMetricCohort = 5
const metricSmoothing = 0.5
const metricBackoff = 5.0
const metricWindowTokens = 128
const maxMetricWindows = 32

type metricToken struct {
	raw, normalized, file string
	line, endLine         int
}

// Ignore directives and comments; preserve identifier identity without its spelling.
func lexicalTokens(root *sitter.Node, content []byte, file string) []metricToken {
	var tokens []metricToken
	identifiers := map[string]string{}
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if node.IsMissing() {
			return
		}
		kind := node.Type()
		if kind == "comment" || kind == "preproc_include" || kind == "preproc_def" || kind == "preproc_function_def" || kind == "preproc_call" {
			return
		}
		if strings.HasPrefix(kind, "preproc_") {
			for i := 0; i < int(node.NamedChildCount()); i++ {
				child := node.NamedChild(i)
				if child.Type() != "identifier" && child.Type() != "binary_expression" && child.Type() != "number_literal" {
					visit(child)
				}
			}
			return
		}
		switch kind {
		case "string_literal", "char_literal", "system_lib_string", "number_literal":
			value := "<" + kind + ">"
			tokens = append(tokens, metricToken{value, value, file, int(node.StartPoint().Row) + 1, int(node.EndPoint().Row) + 1})
			return
		}
		if node.ChildCount() == 0 {
			raw := node.Content(content)
			if raw == "" {
				return
			}
			normalized := raw
			if kind == "identifier" || kind == "field_identifier" || kind == "type_identifier" {
				key := kind + ":" + raw
				if identifiers[key] == "" {
					identifiers[key] = fmt.Sprintf("<id:%d>", len(identifiers))
				}
				normalized = identifiers[key]
			}
			tokens = append(tokens, metricToken{raw, normalized, file, int(node.StartPoint().Row) + 1, int(node.EndPoint().Row) + 1})
			return
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			visit(node.Child(i))
		}
	}
	visit(root)
	return tokens
}

type tokenPair struct{ previous, token string }
type tokenCounts struct {
	total    int
	tokens   map[string]int
	contexts map[string]int
	pairs    map[tokenPair]int
}

func countTokens(files [][]metricToken, normalized bool) tokenCounts {
	c := tokenCounts{tokens: map[string]int{}, contexts: map[string]int{}, pairs: map[tokenPair]int{}}
	for _, tokens := range files {
		previous := "<start>"
		for _, item := range tokens {
			token := item.raw
			if normalized {
				token = item.normalized
			}
			c.total++
			c.tokens[token]++
			c.contexts[previous]++
			c.pairs[tokenPair{previous, token}]++
			previous = token
		}
	}
	return c
}

func tokenEntropy(counts tokenCounts) float64 {
	keys := make([]string, 0, len(counts.tokens))
	for token := range counts.tokens {
		keys = append(keys, token)
	}
	sort.Strings(keys)
	h := 0.0
	for _, token := range keys {
		p := float64(counts.tokens[token]) / float64(counts.total)
		h -= p * math.Log2(p)
	}
	return round3(h)
}

func tokenSignature(files [][]metricToken, normalized bool) string {
	h := sha256.New()
	for _, tokens := range files {
		h.Write([]byte("\x00file\x00"))
		for _, token := range tokens {
			value := token.raw
			if normalized {
				value = token.normalized
			}
			fmt.Fprintf(h, "%d:%s", len(value), value)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

type metricMoments struct {
	n        int
	mean, m2 float64
}

func (m *metricMoments) add(value float64) {
	m.n++
	delta := value - m.mean
	m.mean += delta / float64(m.n)
	m.m2 += delta * (value - m.mean)
}
func (m metricMoments) stddev() float64 {
	if m.n == 0 {
		return 0
	}
	return math.Sqrt(math.Max(0, m.m2/float64(m.n)))
}

func peerSurprisals(files [][]metricToken, corpus, excluded tokenCounts, normalized bool) ([][]float64, metricMoments, float64) {
	vocabulary := 1
	for token, count := range corpus.tokens {
		if count > excluded.tokens[token] {
			vocabulary++
		}
	}
	var series [][]float64
	var moments metricMoments
	unknown := 0
	for _, tokens := range files {
		previous := "<start>"
		values := make([]float64, 0, len(tokens))
		for _, item := range tokens {
			token := item.raw
			if normalized {
				token = item.normalized
			}
			if corpus.tokens[token] == excluded.tokens[token] {
				token = "<unknown>"
				unknown++
			}
			unigram := (float64(corpus.tokens[token]-excluded.tokens[token]) + metricSmoothing) /
				(float64(corpus.total-excluded.total) + metricSmoothing*float64(vocabulary))
			pair := tokenPair{previous, token}
			p := (float64(corpus.pairs[pair]-excluded.pairs[pair]) + metricBackoff*unigram) /
				(float64(corpus.contexts[previous]-excluded.contexts[previous]) + metricBackoff)
			surprisal := -math.Log2(p)
			values = append(values, surprisal)
			moments.add(surprisal)
			previous = token
		}
		series = append(series, values)
	}
	return series, moments, float64(unknown) / float64(moments.n)
}

func addCounts(corpus *tokenCounts, counts tokenCounts) {
	corpus.total += counts.total
	for key, count := range counts.tokens {
		corpus.tokens[key] += count
	}
	for key, count := range counts.contexts {
		corpus.contexts[key] += count
	}
	for key, count := range counts.pairs {
		corpus.pairs[key] += count
	}
}

// Hold out every equivalent submission and count each remaining structure once.
func setTokenMetrics(results []domain.AISubmissionResult, files [][][]metricToken) {
	rawCounts, normalizedCounts := make([]tokenCounts, len(files)), make([]tokenCounts, len(files))
	groups := map[string]int{}
	signatures := make([]string, len(files))
	eligible := 0
	for i, source := range files {
		rawCounts[i], normalizedCounts[i] = countTokens(source, false), countTokens(source, true)
		if rawCounts[i].total < minPeerTokens {
			continue
		}
		eligible++
		signature := tokenSignature(source, true)
		signatures[i] = signature
		previous, exists := groups[signature]
		if !exists || tokenSignature(source, false) < tokenSignature(files[previous], false) {
			groups[signature] = i
		}
	}
	rawCorpus, normalizedCorpus := countTokens(nil, false), countTokens(nil, true)
	for _, i := range groups {
		addCounts(&rawCorpus, rawCounts[i])
		addCounts(&normalizedCorpus, normalizedCounts[i])
	}
	for i, counts := range rawCounts {
		if counts.total < minMetricTokens {
			continue
		}
		normalizedEntropy := tokenEntropy(normalizedCounts[i])
		metrics := &domain.AITokenMetrics{TokenCount: counts.total, EntropyBits: tokenEntropy(counts),
			NormalizedEntropyBits: &normalizedEntropy, Method: "peer-bigram-v2", CohortSize: max(0, len(groups)-1)}
		results[i].TokenMetrics = metrics
		if counts.total < minPeerTokens {
			metrics.CohortSize = len(groups)
			continue
		}
		metrics.ExcludedPeers = eligible - 1 - metrics.CohortSize
		if len(groups) < minMetricCohort {
			continue
		}
		representative := groups[signatures[i]]
		_, rawMoments, unknown := peerSurprisals(files[i], rawCorpus, rawCounts[representative], false)
		series, normalizedMoments, _ := peerSurprisals(files[i], normalizedCorpus, normalizedCounts[representative], true)
		crossEntropy, perplexity, stddev := round3(rawMoments.mean), round3(math.Exp2(rawMoments.mean)), round3(rawMoments.stddev())
		normalizedCrossEntropy, normalizedPerplexity := round3(normalizedMoments.mean), round3(math.Exp2(normalizedMoments.mean))
		unknown = round3(unknown)
		metrics.CrossEntropyBits, metrics.Perplexity, metrics.SurprisalStddevBits = &crossEntropy, &perplexity, &stddev
		metrics.NormalizedCrossEntropyBits, metrics.NormalizedPerplexity, metrics.UnknownTokenShare = &normalizedCrossEntropy, &normalizedPerplexity, &unknown
		setLocalTokenWindows(metrics, files[i], series)
	}
}

func setLocalTokenWindows(metrics *domain.AITokenMetrics, files [][]metricToken, series [][]float64) {
	var windows []domain.AITokenWindow
	var moments metricMoments
	changeSum, changeCount := 0.0, 0
	for fileIndex, tokens := range files {
		if len(tokens) < metricWindowTokens/2 {
			continue
		}
		previous := -1.0
		for start := 0; start < len(tokens); {
			end := min(start+metricWindowTokens, len(tokens))
			if len(tokens)-end < metricWindowTokens/2 {
				end = len(tokens)
			}
			local := metricMoments{}
			for _, value := range series[fileIndex][start:end] {
				local.add(value)
			}
			mean := local.mean
			moments.add(mean)
			if previous >= 0 {
				changeSum += math.Abs(mean - previous)
				changeCount++
			}
			previous = mean
			windows = append(windows, domain.AITokenWindow{File: tokens[start].file, StartLine: tokens[start].line,
				EndLine: tokens[end-1].endLine, TokenCount: end - start, CrossEntropyBits: round3(mean), Perplexity: round3(math.Exp2(mean)),
				EntropyBits: tokenEntropy(countTokens([][]metricToken{tokens[start:end]}, true)), SurprisalStddevBits: round3(local.stddev())})
			start = end
		}
	}
	metrics.WindowCount = len(windows)
	if len(windows) >= 2 {
		value := round3(moments.stddev())
		metrics.WindowStddevBits = &value
		burstiness := round3((moments.stddev() - moments.mean) / (moments.stddev() + moments.mean))
		metrics.WindowBurstiness = &burstiness
	}
	if changeCount > 0 {
		value := round3(changeSum / float64(changeCount))
		metrics.AdjacentWindowChangeBits = &value
	}
	if len(windows) > maxMetricWindows {
		sort.SliceStable(windows, func(i, j int) bool {
			return math.Abs(windows[i].CrossEntropyBits-moments.mean) > math.Abs(windows[j].CrossEntropyBits-moments.mean)
		})
		windows = windows[:maxMetricWindows]
	}
	sort.SliceStable(windows, func(i, j int) bool {
		if windows[i].File != windows[j].File {
			return windows[i].File < windows[j].File
		}
		return windows[i].StartLine < windows[j].StartLine
	})
	metrics.Windows = windows
}
