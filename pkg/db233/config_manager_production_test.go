package db233

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func TestConfigManagerLoadsTypedEnvironmentValues(t *testing.T) {
	t.Setenv("DB233_CONFIG_PORT", "23390")
	t.Setenv("DB233_CONFIG_ENABLED", "true")
	t.Setenv("DB233_OTHER_SECRET", "must-not-load")
	manager := &ConfigManager{configs: make(map[string]any)}
	manager.LoadFromEnv("DB233_CONFIG")
	if got := manager.GetInt("PORT", 0); got != 23390 {
		t.Fatalf("PORT = %d, want 23390", got)
	}
	if !manager.GetBool("ENABLED", false) {
		t.Fatal("ENABLED 未解析为 true")
	}
	if got := manager.GetString("DB233_OTHER_SECRET", "missing"); got != "missing" {
		t.Fatal("加载了前缀外环境变量")
	}
}

func TestConfigManagerSnapshotsDoNotAliasNestedValues(t *testing.T) {
	manager := &ConfigManager{configs: make(map[string]any)}
	original := map[string]any{"nested": []any{"safe"}}
	manager.Set("object", original)
	original["nested"].([]any)[0] = "caller-mutated"

	snapshot := manager.GetAll()
	object := snapshot["object"].(map[string]any)
	if got := object["nested"].([]any)[0]; got != "safe" {
		t.Fatalf("Set 保留调用方别名: %v", got)
	}
	object["nested"].([]any)[0] = "snapshot-mutated"
	got := manager.GetAll()["object"].(map[string]any)["nested"].([]any)[0]
	if got != "safe" {
		t.Fatalf("GetAll 暴露内部别名: %v", got)
	}
}

func TestProductionSettingsRejectLossyIntegerConversions(t *testing.T) {
	invalid := []any{
		1.5,
		math.NaN(),
		math.Inf(1),
		"10 trailing-data",
		json.Number("1.25"),
		uint64(math.MaxUint64),
	}
	if strconv.IntSize == 32 {
		invalid = append(invalid, int64(math.MaxInt64), uint64(math.MaxUint32))
	}
	for _, value := range invalid {
		if _, err := toInt(value); err == nil {
			t.Fatalf("toInt(%T(%v)) unexpectedly succeeded", value, value)
		}
	}

	for _, value := range []any{42, int64(42), uint64(42), 42.0, json.Number("42"), " 42 "} {
		got, err := toInt(value)
		if err != nil || got != 42 {
			t.Fatalf("toInt(%T(%v)) = %d, %v; want 42", value, value, got, err)
		}
	}
}
