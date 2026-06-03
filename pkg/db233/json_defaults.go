package db233

import (
	"encoding/json"
	"strings"
)

// GetOrCreateDefault parses a JSON string into T. Empty strings, "null", and
// invalid JSON return defaultValue. It is intended for entity hooks that keep a
// string database column and a db:"-" business field.
func GetOrCreateDefault[T any](jsonStr string, defaultValue T) T {
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "" || trimmed == "null" {
		return defaultValue
	}

	var value T
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return defaultValue
	}
	return value
}

// ToJSONStringOrDefault serializes value to JSON. If serialization fails, it
// returns defaultJSON. This pairs with GetOrCreateDefault in custom hooks.
func ToJSONStringOrDefault(value any, defaultJSON string) string {
	if value == nil {
		return defaultJSON
	}
	data, err := json.Marshal(value)
	if err != nil {
		return defaultJSON
	}
	return string(data)
}
