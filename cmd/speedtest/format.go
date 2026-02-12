package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func printTestHeader(r testResult) {
	fmt.Printf("\n=== %s ===\n", styleTestName(r.Name))
	if r.Provider == "cloudflare" {
		fmt.Printf(
			"provider=%s model=%s api_base=%s cloudflare_account_id_ref=%s cloudflare_api_token_ref=%s api_key_ref=%s\n",
			r.Provider,
			r.Model,
			r.APIBase,
			r.CloudflareAccountIDRef,
			r.CloudflareAPITokenRef,
			r.APIKeyRef,
		)
	} else {
		fmt.Printf("provider=%s model=%s api_base=%s api_key_ref=%s\n", r.Provider, r.Model, r.APIBase, r.APIKeyRef)
	}
	printRunTableHeader()
}

func printSetupError(msg string) {
	printRunTableRow("setup", "", "error", "false")
	fmt.Printf("  err: %s\n", msg)
}

func printAttemptResult(attempt attemptResult) {
	status := "ok"
	if attempt.Err != "" {
		status = "error"
	}
	printRunTableRow(strconv.Itoa(attempt.Index), formatDurationMS(attempt.Duration), status, strconv.FormatBool(attempt.EchoMatch))
	if attempt.Err != "" {
		fmt.Printf("  err: %s\n", attempt.Err)
	}
	if !attempt.EchoMatch && attempt.Response != "" {
		fmt.Printf("  resp: %q\n", attempt.Response)
	}
}

func printAverage(avg time.Duration) {
	fmt.Printf("avg: %s\n", styleAvg(formatDurationMS(avg)+"ms"))
}

func printRunTableHeader() {
	fmt.Printf("  %8s | %12s | %-6s | %-5s\n", "attempt", "duration_ms", "status", "match")
	fmt.Printf("  %s-+-%s-+-%s-+-%s\n",
		strings.Repeat("-", 8),
		strings.Repeat("-", 12),
		strings.Repeat("-", 6),
		strings.Repeat("-", 5),
	)
}

func printRunTableRow(attempt, durationMS, status, match string) {
	fmt.Printf("  %8s | %12s | %-6s | %-5s\n", attempt, durationMS, status, match)
}

func formatDurationMS(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	return strconv.FormatFloat(ms, 'f', 2, 64)
}

func styleMethod(v string) string {
	return colorize(v, "1;34")
}

func styleTestName(v string) string {
	return colorize(v, "1;36")
}

func styleAvg(v string) string {
	return colorize(v, "1;33")
}

func colorize(v, code string) string {
	if !isColorEnabled() || strings.TrimSpace(v) == "" {
		return v
	}
	return "\033[" + code + "m" + v + "\033[0m"
}

func isColorEnabled() bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	term := strings.TrimSpace(os.Getenv("TERM"))
	return term != "" && term != "dumb"
}
