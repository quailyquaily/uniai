package jsonoutput

import "testing"

func TestNormalizeSingleJSONContentUnwrapsFencedJSON(t *testing.T) {
	input := "```json\n{\n  \"type\": \"final\",\n  \"output\": \"ok\"\n}\n```"
	want := "{\n  \"type\": \"final\",\n  \"output\": \"ok\"\n}"

	got, ok := NormalizeSingleJSONContent(input)
	if !ok {
		t.Fatal("expected fenced json to normalize")
	}
	if got != want {
		t.Fatalf("unexpected normalized json:\n%s", got)
	}
}

func TestNormalizeSingleJSONContentLeavesProseWithFenceAlone(t *testing.T) {
	input := "Here is JSON:\n```json\n{\"ok\":true}\n```"

	if got, ok := NormalizeSingleJSONContent(input); ok {
		t.Fatalf("expected prose with fence to stay unchanged, got %q", got)
	}
}

func TestNormalizeSingleJSONContentLeavesNonJSONFenceAlone(t *testing.T) {
	input := "```text\n{\"ok\":true}\n```"

	if got, ok := NormalizeSingleJSONContent(input); ok {
		t.Fatalf("expected non-json fence to stay unchanged, got %q", got)
	}
}
