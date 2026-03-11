package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	aipkg "github.com/felitrejos/autoscan-engine/pkg/ai"
	"github.com/felitrejos/autoscan-engine/pkg/domain"
	enginepkg "github.com/felitrejos/autoscan-engine/pkg/engine"
	"github.com/felitrejos/autoscan-engine/pkg/policy"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		if err := runCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "similarity":
		if err := similarityCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "ai-detect":
		if err := aiDetectCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	policyPath := fs.String("policy", "", "Path to policy YAML")
	root := fs.String("root", ".", "Root directory containing submissions")
	workers := fs.Int("workers", 0, "Max compile workers (0 = CPU count)")
	outputDir := fs.String("output-dir", "", "Optional output directory for compiled binaries")
	shortNames := fs.Bool("short-names", false, "Use short submission names before first underscore")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *policyPath == "" {
		return fmt.Errorf("-policy is required")
	}

	p, err := policy.Load(*policyPath)
	if err != nil {
		return err
	}

	var opts []enginepkg.CompileOption
	if *workers > 0 {
		opts = append(opts, enginepkg.WithWorkers(*workers))
	}
	if *outputDir != "" {
		opts = append(opts, enginepkg.WithOutputDir(*outputDir))
	}
	opts = append(opts, enginepkg.WithShortNames(*shortNames))

	runner, err := enginepkg.NewRunner(p, opts...)
	if err != nil {
		return err
	}
	defer runner.Cleanup()

	report, err := runner.Run(context.Background(), *root, enginepkg.RunnerCallbacks{})
	if err != nil {
		return err
	}

	return writeJSON(report)
}

func similarityCommand(args []string) error {
	fs := flag.NewFlagSet("similarity", flag.ContinueOnError)
	policyPath := fs.String("policy", "", "Path to policy YAML")
	root := fs.String("root", ".", "Root directory containing submissions")
	sourceFile := fs.String("source-file", "", "Source file to compare (e.g. lab03.c)")
	windowSize := fs.Int("window-size", 6, "Fingerprint window size")
	minTokens := fs.Int("min-func-tokens", 14, "Minimum function tokens")
	threshold := fs.Float64("threshold", 0.6, "Similarity threshold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *policyPath == "" || *sourceFile == "" {
		return fmt.Errorf("-policy and -source-file are required")
	}

	p, err := policy.Load(*policyPath)
	if err != nil {
		return err
	}
	runner, err := enginepkg.NewRunner(p)
	if err != nil {
		return err
	}
	defer runner.Cleanup()

	report, err := runner.Run(context.Background(), *root, enginepkg.RunnerCallbacks{})
	if err != nil {
		return err
	}

	subs := make([]domain.Submission, len(report.Results))
	for i, r := range report.Results {
		subs[i] = r.Submission
	}

	pairs, err := enginepkg.ComputeSimilarityForProcess(subs, *sourceFile, domain.CompareConfig{
		WindowSize:     *windowSize,
		MinFuncTokens:  *minTokens,
		ScoreThreshold: *threshold,
	})
	if err != nil {
		return err
	}

	return writeJSON(pairs)
}

func aiDetectCommand(args []string) error {
	fs := flag.NewFlagSet("ai-detect", flag.ContinueOnError)
	policyPath := fs.String("policy", "", "Path to policy YAML")
	root := fs.String("root", ".", "Root directory containing submissions")
	sourceFile := fs.String("source-file", "", "Source file to compare (e.g. lab03.c)")
	dictionaryPath := fs.String("dictionary", "", "Path to AI dictionary YAML")
	windowSize := fs.Int("window-size", 6, "Fingerprint window size")
	minTokens := fs.Int("min-func-tokens", 6, "Minimum function tokens")
	threshold := fs.Float64("threshold", 0.6, "AI score threshold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *policyPath == "" || *sourceFile == "" || *dictionaryPath == "" {
		return fmt.Errorf("-policy, -source-file, and -dictionary are required")
	}

	dict, err := aipkg.LoadDictionary(*dictionaryPath)
	if err != nil {
		return err
	}

	p, err := policy.Load(*policyPath)
	if err != nil {
		return err
	}
	runner, err := enginepkg.NewRunner(p)
	if err != nil {
		return err
	}
	defer runner.Cleanup()

	report, err := runner.Run(context.Background(), *root, enginepkg.RunnerCallbacks{})
	if err != nil {
		return err
	}

	subs := make([]domain.Submission, len(report.Results))
	for i, r := range report.Results {
		subs[i] = r.Submission
	}

	aiReport, err := enginepkg.ComputeAIDetectionForProcess(subs, *sourceFile, dict, domain.CompareConfig{
		WindowSize:     *windowSize,
		MinFuncTokens:  *minTokens,
		ScoreThreshold: *threshold,
	})
	if err != nil {
		return err
	}

	return writeJSON(aiReport)
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: autoscan-engine <run|similarity|ai-detect> [flags]")
}
