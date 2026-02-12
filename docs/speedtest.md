# speedtest Package Guide

`speedtest` is a reusable package in `uniai` for benchmarking multiple `*uniai.Client` instances with a consistent flow and result format.

This repository has two related directories:

- `speedtest/`: the Go package for direct use in your code (this document).
- `cmd/speedtest/`: a CLI built on top of the package.

If you only need quick speed checks, you can use the built-in CLI directly:

- See [`cmd/speedtest/README.md`](../cmd/speedtest/README.md).

## Quick Start

```go
package main

import (
	"context"
	"log"

	"github.com/quailyquaily/uniai"
	"github.com/quailyquaily/uniai/speedtest"
)

func main() {
	temp := 0.0

	openaiClient := uniai.New(uniai.Config{
		Provider:     "openai",
		OpenAIAPIKey: "YOUR_OPENAI_KEY",
		OpenAIModel:  "gpt-4o-mini",
	})

	openrouterClient := uniai.New(uniai.Config{
		Provider:      "openai",
		OpenAIAPIKey:  "YOUR_OPENROUTER_KEY",
		OpenAIAPIBase: "https://openrouter.ai/api/v1",
		OpenAIModel:   "openai/gpt-4o-mini",
	})

	report, err := speedtest.Run(
		context.Background(),
		[]speedtest.Case{
			{Name: "openai", Client: openaiClient},
			{Name: "openrouter", Client: openrouterClient},
		},
		speedtest.Config{
			Method:      speedtest.MethodEcho,
			Attempts:    3,
			Temperature: &temp,
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	for _, r := range report.Results {
		log.Printf("%s avg=%s setup_error=%s", r.Name, r.Average, r.SetupError)
	}
}
```

## API Overview

- Entry point: `speedtest.Run(ctx, cases, cfg)`
- Test case: `speedtest.Case{Name, Client}`
- Result root: `speedtest.Report{Method, Results}`
- Per-case result: `speedtest.CaseResult`
- Per-attempt result: `speedtest.AttemptResult`

## Config Fields and Defaults

- `Method`: test method. Supported values: `speedtest.MethodEcho`, `speedtest.MethodToolCalling`. Default: `echo`.
- `Attempts`: number of attempts per case. Default: `3`.
- `Timeout`: timeout per attempt. Default: `90s`.
- `Temperature`: optional temperature. Default: `1.0`.
- `EchoText`: target text for `echo` method. Default: `"speedtest-echo-20260211"`.
- `OnEvent`: optional callback for progress events.

## Method Semantics

- `echo`: a strict "Linux `echo`-like" baseline for LLM output stability.
  The model is instructed to output exactly the user text (`EchoText`) with no extra characters.
  Match rule is exact equality of returned text vs input text.
- `toolcalling`: injects 3 mock tools (`get_weather`, `get_direction`, `send_message`) and checks whether the model chooses `get_direction` for the route query ("How do I get from Tokyo Station to Shinjuku Station?").

## Event Callback

`OnEvent` receives these `EventType` values:

- `case_start`: a case starts.
- `attempt_done`: one attempt finishes.
- `case_done`: a case completes.

A common pattern is to print `attempt_done` events for live progress.

## Result Fields

- `CaseResult.SetupError`: setup failure (for example nil `client` or missing model); no request is sent for that case.
- `CaseResult.Attempts`: detailed attempt list including duration, success, match, error, and response excerpt.
- `CaseResult.Average`: average duration across attempts for the case.

## Notes

- `speedtest` reads provider/model/api_base from `client.GetConfig()`, so your `Client` should have a model configured.
- The package does not persist results by itself. For CSV output, use `cmd/speedtest/` or serialize `Report` yourself.
- The `echo` and `toolcalling` checks are fixed baseline rules, not a general evaluation framework.
