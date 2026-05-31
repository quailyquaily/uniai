package uniai

import "github.com/quailyquaily/uniai/internal/jsonoutput"

// StripNonJSONLines removes lines that are unlikely to be part of a JSON payload.
// It keeps multi-line JSON blocks intact by tracking brace/bracket depth.
func StripNonJSONLines(input string) string {
	return jsonoutput.StripNonJSONLines(input)
}

// AttemptJSONRepair applies minimal fixes for common JSON issues like trailing commas,
// unclosed quotes, and missing closing braces/brackets.
func AttemptJSONRepair(input string) string {
	return jsonoutput.AttemptRepair(input)
}

// FindJSONSnippets scans text and returns all valid JSON substrings it can find.
func FindJSONSnippets(text string) []string {
	return jsonoutput.FindSnippets(text)
}

// CollectJSONCandidates extracts possible JSON payloads from text, including code fences
// and embedded JSON snippets.
func CollectJSONCandidates(text string) ([]string, error) {
	return jsonoutput.CollectCandidates(text)
}
