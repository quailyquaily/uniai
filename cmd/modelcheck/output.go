package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/quailyquaily/uniai"
)

// ANSI SGR codes.
const (
	ansiBold   = "1"
	ansiDim    = "2"
	ansiRed    = "31"
	ansiGreen  = "32"
	ansiYellow = "33"
	ansiCyan   = "36"
)

// painter wraps text in ANSI color codes when enabled.
type painter struct{ enabled bool }

func (p painter) paint(code, s string) string {
	if !p.enabled || code == "" || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func statusColor(st status) string {
	switch st {
	case statusPass:
		return ansiGreen
	case statusFail:
		return ansiRed + ";" + ansiBold
	case statusSkip:
		return ansiYellow
	}
	return ""
}

// colorEnabled follows the common conventions: --color always/never wins;
// otherwise color only a terminal, and respect NO_COLOR and TERM=dumb.
func colorEnabled(mode string, out io.Writer, getenv func(string) string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	if getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// cell is one table value with an optional color applied after padding, so
// escape codes never affect column alignment.
type cell struct {
	text  string
	color string
}

func writeTable(out io.Writer, p painter, header []string, rows [][]cell) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if n := utf8.RuneCountInString(c.text); n > widths[i] {
				widths[i] = n
			}
		}
	}
	line := func(cells []cell) {
		var b strings.Builder
		for i, c := range cells {
			b.WriteString(p.paint(c.color, c.text))
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c.text)+2))
			}
		}
		fmt.Fprintln(out, b.String())
	}
	head := make([]cell, len(header))
	for i, h := range header {
		head[i] = cell{h, ansiBold}
	}
	line(head)
	for _, row := range rows {
		line(row)
	}
}

// hitRateLabel shows the cache hit rate inside the CACHE column.
func hitRateLabel(c caseResult) string {
	if c.CacheHitRate == nil {
		return ""
	}
	return fmt.Sprintf(" %.1f%%", *c.CacheHitRate*100)
}

func progressLine(p painter, r modelResult) string {
	prefix := p.paint(ansiDim, "done") + "  " + r.Name
	if r.Skip != "" {
		return prefix + "  " + p.paint(ansiYellow, "SKIP ("+r.Skip+")")
	}
	parts := make([]string, 0, len(r.Cases))
	for _, c := range r.Cases {
		parts = append(parts, c.Case+"="+p.paint(statusColor(c.Status), string(c.Status)))
	}
	return prefix + "  " + strings.Join(parts, " ")
}

func printList(out io.Writer, p painter, models []modelConfig, getenv func(string) string) {
	catalog := uniai.DefaultPricingCatalog()
	rows := make([][]cell, 0, len(models))
	for _, m := range models {
		r, skip := resolveEnv(m, getenv)
		key := cell{r.keyEnv, ansiGreen}
		if skip != "" {
			key = cell{"SKIP: " + skip, ansiYellow}
		}
		base := cell{r.apiBase, ""}
		if base.text == "" {
			base = cell{"(provider default)", ansiDim}
		}
		price := cell{"unpriced", ansiRed}
		if info, ok := catalogPrice(catalog, m.Model); ok {
			price = cell{info.String(), ""}
			if info.cached == nil {
				price.color = ansiYellow
			}
		}
		rows = append(rows, []cell{{m.Name, ansiCyan}, {m.Provider, ""}, {m.Model, ""}, key, base, price})
	}
	writeTable(out, p, []string{"NAME", "PROVIDER", "MODEL", "KEY", "API BASE", "PRICE"}, rows)
}

func printTable(out io.Writer, p painter, results []modelResult, cases []string) {
	header := []string{"MODEL", "PROVIDER"}
	for _, c := range cases {
		header = append(header, strings.ToUpper(c))
	}
	header = append(header, "COST")

	rows := make([][]cell, 0, len(results))
	for _, r := range results {
		nameColor := ansiCyan
		if r.Skip != "" {
			nameColor = ansiDim
		}
		row := []cell{{r.Name, nameColor}, {r.Provider, ""}}
		byCase := map[string]caseResult{}
		var cost float64
		var priced bool
		for _, c := range r.Cases {
			byCase[c.Case] = c
			cost += c.CostUSD
			priced = priced || c.Priced
		}
		for _, name := range cases {
			c, ok := byCase[name]
			switch {
			case !ok:
				row = append(row, cell{"-", ansiDim})
			case c.Status == statusPass:
				row = append(row, cell{"PASS" + hitRateLabel(c) + fmt.Sprintf(" %.1fs", c.Duration.Seconds()), statusColor(c.Status)})
			default:
				row = append(row, cell{string(c.Status) + hitRateLabel(c), statusColor(c.Status)})
			}
		}
		if priced {
			row = append(row, cell{fmt.Sprintf("$%.4f", cost), ""})
		} else {
			row = append(row, cell{"-", ansiDim})
		}
		rows = append(rows, row)
	}
	writeTable(out, p, header, rows)
}

func printDetails(out io.Writer, p painter, results []modelResult, verbose bool) {
	header := false
	for _, r := range results {
		for _, c := range r.Cases {
			show := c.Status == statusFail || (verbose && c.Status != statusSkip) || (c.Case == caseCache && c.Status == statusPass)
			if !show {
				continue
			}
			if !header {
				fmt.Fprintln(out, "\n"+p.paint(ansiBold, "Details:"))
				header = true
			}
			fmt.Fprintf(out, "- %s %s %s: %s\n", p.paint(ansiCyan, r.Name), c.Case, p.paint(statusColor(c.Status), string(c.Status)), c.Detail)
			if (c.Status == statusFail || verbose) && strings.TrimSpace(c.Response) != "" {
				fmt.Fprintln(out, p.paint(ansiDim, "    response: "+truncate(c.Response, 300)))
			}
		}
	}
	var skipped []string
	for _, r := range results {
		if r.Skip != "" {
			skipped = append(skipped, r.Name)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintln(out, "\n"+p.paint(ansiYellow, "Skipped (missing credentials): "+strings.Join(skipped, ", ")))
	}
}

// printSummary prints one colored line with case totals.
func printSummary(out io.Writer, p painter, results []modelResult) {
	counts := map[status]int{}
	for _, r := range results {
		for _, c := range r.Cases {
			counts[c.Status]++
		}
	}
	parts := []string{
		p.paint(statusColor(statusPass), fmt.Sprintf("%d passed", counts[statusPass])),
		p.paint(statusColor(statusFail), fmt.Sprintf("%d failed", counts[statusFail])),
		p.paint(statusColor(statusSkip), fmt.Sprintf("%d skipped", counts[statusSkip])),
	}
	if counts[statusFail] == 0 {
		parts[1] = p.paint(ansiDim, fmt.Sprintf("%d failed", 0))
	}
	fmt.Fprintf(out, "\n%s %s\n", p.paint(ansiBold, "Result:"), strings.Join(parts, ", "))
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
