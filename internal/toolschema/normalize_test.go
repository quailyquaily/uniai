package toolschema

import "testing"

func TestNormalizeAddsItemsForArrayType(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"body": map[string]any{
				"type": []any{"string", "object", "array"},
			},
		},
	}

	Normalize(schema)

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map")
	}
	body, ok := props["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected body schema map")
	}
	if _, ok := body["items"]; !ok {
		t.Fatalf("expected items to be added for array type")
	}
}
