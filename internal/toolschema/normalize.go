package toolschema

// Normalize mutates the schema in place to satisfy stricter validators.
// Currently it ensures array-typed schemas always include an "items" field.
func Normalize(schema map[string]any) map[string]any {
	normalizeValue(schema)
	return schema
}

func normalizeValue(value any) {
	switch node := value.(type) {
	case map[string]any:
		normalizeMap(node)
	case []any:
		for _, item := range node {
			normalizeValue(item)
		}
	}
}

func normalizeMap(node map[string]any) {
	if includesArrayType(node["type"]) {
		if _, ok := node["items"]; !ok || node["items"] == nil {
			node["items"] = map[string]any{}
		}
	}

	for _, key := range []string{"properties", "patternProperties", "definitions", "$defs"} {
		if props, ok := node[key].(map[string]any); ok {
			for _, val := range props {
				normalizeValue(val)
			}
		}
	}

	for _, key := range []string{"items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames"} {
		if val, ok := node[key]; ok {
			normalizeValue(val)
		}
	}

	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if items, ok := node[key].([]any); ok {
			for _, val := range items {
				normalizeValue(val)
			}
		}
	}
}

func includesArrayType(value any) bool {
	switch t := value.(type) {
	case string:
		return t == "array"
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s == "array" {
				return true
			}
		}
	case []string:
		for _, item := range t {
			if item == "array" {
				return true
			}
		}
	}
	return false
}
