package engine

import (
	"math"
	"sort"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	sitter "github.com/smacker/go-tree-sitter"
)

const minMetricTokens = 50
const minMetricCohort = 5
const metricSmoothing = 0.5

// Keep identifier spelling; discard comments and normalize literal contents.
func lexicalTokens(root *sitter.Node, content []byte) []string {
	var tokens []string
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if node.IsMissing() {
			return
		}
		switch node.Type() {
		case "comment":
			return
		case "string_literal", "char_literal", "system_lib_string", "number_literal":
			tokens = append(tokens, "<"+node.Type()+">")
			return
		}
		if node.ChildCount() == 0 {
			if text := node.Content(content); text != "" {
				tokens = append(tokens, text)
			}
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

func countTokens(files [][]string) tokenCounts {
	c := tokenCounts{tokens: map[string]int{}, contexts: map[string]int{}, pairs: map[tokenPair]int{}}
	for _, tokens := range files {
		previous := "<start>"
		for _, token := range tokens {
			c.total++
			c.tokens[token]++
			c.contexts[previous]++
			c.pairs[tokenPair{previous, token}]++
			previous = token
		}
	}
	return c
}

// Leave the entire submission out of the peer bigram model; metrics never affect flags.
func setTokenMetrics(results []domain.AISubmissionResult, files [][][]string) {
	counts := make([]tokenCounts, len(files))
	corpus := countTokens(nil)
	eligible := 0
	for i, source := range files {
		counts[i] = countTokens(source)
		if counts[i].total < minMetricTokens {
			continue
		}
		eligible++
		for k, v := range counts[i].tokens {
			corpus.tokens[k] += v
		}
		for k, v := range counts[i].contexts {
			corpus.contexts[k] += v
		}
		for k, v := range counts[i].pairs {
			corpus.pairs[k] += v
		}
	}
	for i, c := range counts {
		if c.total < minMetricTokens {
			continue
		}
		keys := make([]string, 0, len(c.tokens))
		for token := range c.tokens {
			keys = append(keys, token)
		}
		sort.Strings(keys)
		entropy := 0.0
		for _, token := range keys {
			p := float64(c.tokens[token]) / float64(c.total)
			entropy -= p * math.Log2(p)
		}
		metrics := &domain.AITokenMetrics{TokenCount: c.total, EntropyBits: round3(entropy), CohortSize: eligible - 1}
		results[i].TokenMetrics = metrics
		if eligible < minMetricCohort {
			continue
		}
		vocabulary := 1 // One smoothed bucket for all unseen tokens.
		for token, n := range corpus.tokens {
			if n > c.tokens[token] {
				vocabulary++
			}
		}
		mean, m2, n := 0.0, 0.0, 0
		for _, tokens := range files[i] {
			previous := "<start>"
			for _, raw := range tokens {
				token := raw
				if corpus.tokens[token] == c.tokens[token] {
					token = "<unknown>"
				}
				pair := tokenPair{previous, token}
				p := (float64(corpus.pairs[pair]-c.pairs[pair]) + metricSmoothing) /
					(float64(corpus.contexts[previous]-c.contexts[previous]) + metricSmoothing*float64(vocabulary))
				surprisal := -math.Log2(p)
				n++
				delta := surprisal - mean
				mean += delta / float64(n)
				m2 += delta * (surprisal - mean)
				previous = token
			}
		}
		crossEntropy, perplexity, stddev := round3(mean), round3(math.Exp2(mean)), round3(math.Sqrt(math.Max(0, m2/float64(n))))
		metrics.CrossEntropyBits, metrics.Perplexity, metrics.SurprisalStddevBits = &crossEntropy, &perplexity, &stddev
	}
}
