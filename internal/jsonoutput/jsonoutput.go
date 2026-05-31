package jsonoutput

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

func StripNonJSONLines(input string) string {
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines))
	depth := 0
	inString := false
	escape := false
	for _, line := range lines {
		keep := true
		if depth == 0 {
			trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
			if !startsWithJSONBrace(trimmed) && !hasJSONBraceWithin(trimmed, 20) {
				keep = false
			}
		}
		if keep {
			out = append(out, line)
		}
		depth, inString, escape = updateJSONDepth(line, depth, inString, escape)
	}
	return strings.Join(out, "\n")
}

func CollectCandidates(text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("empty tool decision")
	}
	candidates := []string{trimmed}
	if strings.Contains(trimmed, "```") {
		parts := strings.Split(trimmed, "```")
		for i := 1; i < len(parts); i += 2 {
			block := strings.TrimSpace(parts[i])
			block = stripCodeFenceLanguage(block)
			if block != "" {
				candidates = append(candidates, block)
			}
		}
	}
	candidates = append(candidates, FindSnippets(trimmed)...)
	if unquoted := UnquoteJSONStringPayload(trimmed); unquoted != "" {
		candidates = append(candidates, unquoted)
		candidates = append(candidates, FindSnippets(unquoted)...)
	}
	return candidates, nil
}

func FindSnippets(text string) []string {
	data := []byte(text)
	var snippets []string
	for i := 0; i < len(data); i++ {
		if data[i] != '{' && data[i] != '[' {
			continue
		}
		if snippet := scanJSONSubstring(data, i); snippet != "" {
			snippets = append(snippets, snippet)
			i += len(snippet) - 1
		}
	}
	return snippets
}

func NormalizeSingleJSONContent(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if isJSONObjectOrArray(trimmed) && json.Valid([]byte(trimmed)) {
		return trimmed, true
	}

	body, ok := singleCodeFenceBody(trimmed)
	if !ok {
		return "", false
	}
	body = strings.TrimSpace(body)
	if !isJSONObjectOrArray(body) {
		return "", false
	}

	candidates, err := CollectCandidates(trimmed)
	if err != nil {
		return "", false
	}
	for _, candidate := range candidates {
		payload := strings.TrimSpace(candidate)
		if unquoted := UnquoteJSONStringPayload(payload); unquoted != "" {
			payload = unquoted
		}
		if payload != body {
			continue
		}
		if json.Valid([]byte(payload)) {
			return payload, true
		}
	}
	return "", false
}

func AttemptRepair(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if !strings.ContainsAny(trimmed, "{[") {
		return ""
	}
	repaired := trailingCommaRe.ReplaceAllString(trimmed, "$1")

	inString := false
	escaped := false
	for i := 0; i < len(repaired); i++ {
		ch := repaired[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
		}
	}
	if inString {
		repaired += `"`
	}

	openBraces := strings.Count(repaired, "{")
	closeBraces := strings.Count(repaired, "}")
	for i := closeBraces; i < openBraces; i++ {
		repaired += "}"
	}

	openBrackets := strings.Count(repaired, "[")
	closeBrackets := strings.Count(repaired, "]")
	for i := closeBrackets; i < openBrackets; i++ {
		repaired += "]"
	}

	return repaired
}

func UnquoteJSONStringPayload(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "\"") {
		return ""
	}
	var value string
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func stripCodeFenceLanguage(block string) string {
	lineEnd := strings.IndexByte(block, '\n')
	if lineEnd == -1 {
		return strings.TrimSpace(block)
	}
	info := strings.TrimSpace(block[:lineEnd])
	if info == "" || strings.EqualFold(info, "json") {
		return strings.TrimSpace(block[lineEnd+1:])
	}
	return strings.TrimSpace(block)
}

func singleCodeFenceBody(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "```") {
		return "", false
	}
	lineEnd := strings.IndexByte(trimmed, '\n')
	if lineEnd == -1 {
		return "", false
	}
	info := strings.TrimSpace(trimmed[3:lineEnd])
	if info != "" && !strings.EqualFold(info, "json") {
		return "", false
	}
	bodyWithClose := strings.TrimRightFunc(trimmed[lineEnd+1:], unicode.IsSpace)
	if !strings.HasSuffix(bodyWithClose, "```") {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimSuffix(bodyWithClose, "```"))
	return body, body != ""
}

func isJSONObjectOrArray(text string) bool {
	return strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")
}

func startsWithJSONBrace(line string) bool {
	return strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[")
}

func hasJSONBraceWithin(line string, limit int) bool {
	if line == "" || limit <= 0 {
		return false
	}
	if len(line) > limit {
		line = line[:limit]
	}
	return strings.ContainsAny(line, "{[")
}

func updateJSONDepth(line string, depth int, inString bool, escape bool) (int, bool, bool) {
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth, inString, escape
}

func scanJSONSubstring(data []byte, start int) string {
	var stack []byte
	inString := false
	escape := false
	for i := start; i < len(data); i++ {
		ch := data[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 {
				return ""
			}
			open := stack[len(stack)-1]
			if (open == '{' && ch != '}') || (open == '[' && ch != ']') {
				return ""
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				snippet := string(data[start : i+1])
				if json.Valid([]byte(snippet)) {
					return snippet
				}
				return ""
			}
		}
	}
	return ""
}

var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)
