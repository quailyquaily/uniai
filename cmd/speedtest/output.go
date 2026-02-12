package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
)

func writeCSV(path string, results []testResult) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv %q: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"test_name",
		"provider",
		"model",
		"attempt",
		"duration_ms",
		"ok",
		"echo_match",
		"average_ms",
	}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	for _, r := range results {
		if r.SetupError != "" {
			row := []string{
				r.Name,
				r.Provider,
				r.Model,
				"setup",
				"",
				"false",
				"false",
				"",
			}
			if err := w.Write(row); err != nil {
				return fmt.Errorf("write csv row: %w", err)
			}
			continue
		}

		for _, attempt := range r.Runs {
			row := []string{
				r.Name,
				r.Provider,
				r.Model,
				strconv.Itoa(attempt.Index),
				formatDurationMS(attempt.Duration),
				strconv.FormatBool(attempt.OK),
				strconv.FormatBool(attempt.EchoMatch),
				"",
			}
			if err := w.Write(row); err != nil {
				return fmt.Errorf("write csv row: %w", err)
			}
		}

		summary := []string{
			r.Name,
			r.Provider,
			r.Model,
			"avg",
			"",
			"",
			"",
			formatDurationMS(r.Average),
		}
		if err := w.Write(summary); err != nil {
			return fmt.Errorf("write csv summary: %w", err)
		}
	}

	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}

	return nil
}
