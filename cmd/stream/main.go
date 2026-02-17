package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/quailyquaily/uniai"
)

const (
	defaultTimeoutSecond = 180
	defaultMaxTokens     = 4096
	defaultPrompt        = "Write a detailed engineering playbook for building and operating a large-scale API platform. Produce exactly 120 numbered items, each item must have 2 full sentences. Every 20 items, add a 5-row markdown table summarizing key risks, signals, and mitigations. Finish with a section titled 'Top 30 Failure Patterns' that lists 30 anti-patterns and one concrete fix for each."
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	modelFlag := fs.String("model", "", "OpenAI model")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: stream [--model model]")
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("openai provider: missing required env OPENAI_API_KEY")
	}

	model := strings.TrimSpace(*modelFlag)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	}
	if model == "" {
		return fmt.Errorf("openai provider: model is required (use --model or OPENAI_MODEL)")
	}

	client := uniai.New(uniai.Config{
		Provider:      "openai",
		OpenAIAPIKey:  apiKey,
		OpenAIAPIBase: strings.TrimSpace(os.Getenv("OPENAI_API_BASE")),
		OpenAIModel:   model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultTimeoutSecond)*time.Second)
	defer cancel()

	start := time.Now()
	streamed := false
	endedWithNewline := false

	resp, err := client.Chat(ctx,
		uniai.WithProvider("openai"),
		uniai.WithModel(model),
		uniai.WithMessages(
			uniai.System("You are a precise technical writer. Follow the requested format exactly and output the full content."),
			uniai.User(defaultPrompt),
		),
		uniai.WithMaxTokens(defaultMaxTokens),
		uniai.WithOnStream(func(ev uniai.StreamEvent) error {
			if ev.Delta != "" {
				streamed = true
				endedWithNewline = strings.HasSuffix(ev.Delta, "\n")
				fmt.Print(ev.Delta)
			}
			if ev.Done && ev.Usage != nil {
				fmt.Fprintf(
					os.Stderr,
					"\nusage: input=%d output=%d total=%d\n",
					ev.Usage.InputTokens,
					ev.Usage.OutputTokens,
					ev.Usage.TotalTokens,
				)
			}
			return nil
		}),
	)
	if err != nil {
		return err
	}

	if !streamed && resp != nil && resp.Text != "" {
		fmt.Print(resp.Text)
		endedWithNewline = strings.HasSuffix(resp.Text, "\n")
	}
	if !endedWithNewline {
		fmt.Println()
	}

	if resp == nil {
		return fmt.Errorf("empty response")
	}

	fmt.Fprintf(
		os.Stderr,
		"done: model=%s elapsed=%s chars=%d\n",
		resp.Model,
		time.Since(start).Round(time.Millisecond),
		len(resp.Text),
	)

	return nil
}
