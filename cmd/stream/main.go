package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/quailyquaily/uniai"
)

const (
	defaultConfigPath   = "config.yaml"
	defaultTimeout      = 180 * time.Second
	defaultMaxTokens    = 2048
	defaultPrompt       = "Find the smallest positive integer divisible by every integer from 1 through 12. Give the result and a brief verification."
	defaultProviderName = "openai"
)

type streamObservation struct {
	reasoning        strings.Builder
	content          strings.Builder
	reasoningChunks  int
	contentChunks    int
	done             bool
	section          string
	endedWithNewline bool
	usage            *uniai.Usage
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to config yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 || rest[0] != "run" || len(rest) > 2 {
		return fmt.Errorf("usage: stream [--config path] run [test_name]")
	}
	selectedName := ""
	if len(rest) == 2 {
		selectedName = rest[1]
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	tests, err := selectTests(cfg.Tests, selectedName)
	if err != nil {
		return err
	}

	type testFailure struct {
		name string
		err  error
	}
	failures := make([]testFailure, 0)
	for i, test := range tests {
		if i > 0 {
			if _, err := fmt.Fprintln(stdout); err != nil {
				return err
			}
		}
		if err := runOne(cfg, test, stdout); err != nil {
			failures = append(failures, testFailure{name: test.Name, err: err})
			if _, writeErr := fmt.Fprintf(stdout, "FAIL: %v\n", err); writeErr != nil {
				return writeErr
			}
		}
	}

	if len(failures) > 0 {
		first := failures[0]
		return fmt.Errorf("%d stream reasoning test(s) failed; %s: %w", len(failures), first.name, first.err)
	}
	return nil
}

func runOne(cfg *fileConfig, test testConfig, stdout io.Writer) error {
	clientConfig, err := buildClientConfig(test)
	if err != nil {
		return err
	}

	prompt := strings.TrimSpace(test.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(cfg.Prompt)
	}
	if prompt == "" {
		prompt = defaultPrompt
	}

	maxTokens := test.MaxTokens
	if maxTokens == 0 {
		maxTokens = cfg.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	timeout := test.TimeoutSeconds
	if timeout == 0 {
		timeout = cfg.TimeoutSeconds
	}
	requestTimeout := defaultTimeout
	if timeout > 0 {
		requestTimeout = time.Duration(timeout) * time.Second
	}

	provider := test.Provider
	if provider == "" {
		provider = defaultProviderName
	}
	if _, err := fmt.Fprintf(
		stdout,
		"%s (%s / %s)\n\n",
		test.Name,
		provider,
		test.Model,
	); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	observation := streamObservation{}
	writeStreamText := func(section, delta string) error {
		if delta == "" {
			return nil
		}
		if observation.section != section {
			if observation.section != "" {
				if !observation.endedWithNewline {
					if _, err := fmt.Fprintln(stdout); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintln(stdout); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(stdout, "%s:\n", section); err != nil {
				return err
			}
			observation.section = section
			observation.endedWithNewline = true
		}
		if _, err := fmt.Fprint(stdout, delta); err != nil {
			return err
		}
		observation.endedWithNewline = strings.HasSuffix(delta, "\n")
		return nil
	}
	options := []uniai.ChatOption{
		uniai.WithProvider(provider),
		uniai.WithModel(test.Model),
		uniai.WithMessages(uniai.User(prompt)),
		uniai.WithMaxTokens(maxTokens),
		uniai.WithReasoningDetails(),
		uniai.WithOnStream(func(event uniai.StreamEvent) error {
			if event.ReasoningDelta != nil && event.ReasoningDelta.Delta != "" {
				observation.reasoningChunks++
				observation.reasoning.WriteString(event.ReasoningDelta.Delta)
				if err := writeStreamText("Reasoning", event.ReasoningDelta.Delta); err != nil {
					return err
				}
			}
			if event.Delta != "" {
				observation.contentChunks++
				observation.content.WriteString(event.Delta)
				if err := writeStreamText("Answer", event.Delta); err != nil {
					return err
				}
			}
			if event.Done {
				observation.done = true
				if event.Usage != nil {
					usage := *event.Usage
					observation.usage = &usage
				}
			}
			return nil
		}),
	}
	if test.ReasoningEffort != "" {
		options = append(options, uniai.WithReasoningEffort(uniai.ReasoningEffort(test.ReasoningEffort)))
	}
	if test.ReasoningBudgetTokens != nil {
		options = append(options, uniai.WithReasoningBudgetTokens(*test.ReasoningBudgetTokens))
	}

	client := uniai.New(clientConfig)
	startedAt := time.Now()
	response, err := client.Chat(ctx, options...)
	if observation.section != "" {
		if !observation.endedWithNewline {
			if _, writeErr := fmt.Fprintln(stdout); writeErr != nil && err == nil {
				err = writeErr
			}
		}
		if _, writeErr := fmt.Fprintln(stdout); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	if err != nil {
		return err
	}
	if response == nil {
		return fmt.Errorf("provider returned an empty response")
	}
	if !observation.done {
		return fmt.Errorf("stream ended without a done event")
	}
	if strings.TrimSpace(observation.reasoning.String()) == "" {
		return fmt.Errorf("no non-empty reasoning delta received through WithOnStream")
	}
	if observation.usage != nil {
		if _, err := fmt.Fprintf(
			stdout,
			"Usage: input %d, output %d, total %d tokens\n",
			observation.usage.InputTokens,
			observation.usage.OutputTokens,
			observation.usage.TotalTokens,
		); err != nil {
			return err
		}
	}

	reasoningChunkLabel := "chunks"
	if observation.reasoningChunks == 1 {
		reasoningChunkLabel = "chunk"
	}
	contentChunkLabel := "chunks"
	if observation.contentChunks == 1 {
		contentChunkLabel = "chunk"
	}

	_, err = fmt.Fprintf(
		stdout,
		"PASS: reasoning %d chars (%d %s), answer %d chars (%d %s), elapsed %s\n",
		utf8.RuneCountInString(observation.reasoning.String()),
		observation.reasoningChunks,
		reasoningChunkLabel,
		utf8.RuneCountInString(observation.content.String()),
		observation.contentChunks,
		contentChunkLabel,
		time.Since(startedAt).Round(time.Millisecond),
	)
	return err
}
