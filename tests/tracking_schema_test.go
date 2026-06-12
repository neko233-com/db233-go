package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestTrackingSchemaLoadValidateAndPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracking.json")
	content := `
{
  // JSONC: allow comments in .json file.
  "version": "1",
  "tables": [
    {
      "name": "player_behavior_events",
      "comment": "player behavior tracking",
      "columns": [
        {"name": "player_id", "type": "string", "size": 64, "primaryKey": true, "required": true},
        {"name": "event_time", "type": "timestamp", "required": true, "default": "CURRENT_TIMESTAMP"},
        {"name": "action", "type": "string", "size": 64, "required": true, "enum": ["login", "level_up"]},
        {"name": "level", "type": "int"},
        {"name": "extra", "type": "json", "comment": "url like https://example.com must survive"}
      ],
      /*
       * Block comment also supported.
       */
      "indexes": [
        {"name": "idx_player_behavior_action", "columns": ["action"]}
      ]
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入描述文件失败: %v", err)
	}

	schema, hash, err := db233.LoadTrackingSchemaFile(path)
	if err != nil {
		t.Fatalf("加载描述文件失败: %v", err)
	}
	if hash == "" {
		t.Fatal("hash 不应为空")
	}
	table, ok := schema.GetTable("player_behavior_events")
	if !ok {
		t.Fatal("应能获取表描述")
	}

	validPayload := map[string]any{
		"player_id":  "p001",
		"event_time": "2026-06-12T10:00:00Z",
		"action":     "login",
		"level":      10,
		"extra":      map[string]any{"client": "android"},
	}
	if errs := table.ValidatePayload(validPayload, false); len(errs) != 0 {
		t.Fatalf("合法 payload 不应报错: %v", errs)
	}
	extra, ok := table.GetColumn("extra")
	if !ok || !strings.Contains(extra.Comment, "https://example.com") {
		t.Fatalf("字符串内 URL 不应被注释解析破坏: %#v", extra)
	}

	invalidPayload := map[string]any{
		"player_id": "p001",
		"action":    "unknown",
		"level":     "10",
		"new_field": true,
	}
	errs := table.ValidatePayload(invalidPayload, false)
	if len(errs) < 3 {
		t.Fatalf("非法 payload 应返回多个错误，得到: %v", errs)
	}
}

func TestTrackingSchemaBuildInsertSQL(t *testing.T) {
	schema := &db233.TrackingSchema{
		Version: "1",
		Tables: []db233.TrackingTable{
			{
				Name: "player_behavior_events",
				Columns: []db233.TrackingColumn{
					{Name: "player_id", Type: "string", PrimaryKey: true, Required: true},
					{Name: "action", Type: "string", Required: true},
					{Name: "score", Type: "int"},
				},
			},
		},
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("schema 应合法: %v", err)
	}
	table, _ := schema.GetTable("player_behavior_events")
	sql, values, err := table.BuildTrackingInsertSQL(map[string]any{
		"player_id": "p001",
		"action":    "level_up",
		"score":     100,
	})
	if err != nil {
		t.Fatalf("生成 insert 失败: %v", err)
	}
	if !strings.Contains(sql, "INSERT INTO `player_behavior_events`") {
		t.Fatalf("SQL 表名错误: %s", sql)
	}
	if len(values) != 3 {
		t.Fatalf("参数数量错误: %d", len(values))
	}
}

func TestTrackingSchemaRejectsInvalidName(t *testing.T) {
	schema := &db233.TrackingSchema{
		Tables: []db233.TrackingTable{
			{
				Name: "bad-name",
				Columns: []db233.TrackingColumn{
					{Name: "player_id", Type: "string", PrimaryKey: true},
				},
			},
		},
	}
	if err := schema.Validate(); err == nil {
		t.Fatal("非法表名应报错")
	}
}

func TestTrackingSchemaApplyAndInsertIntegration(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	db.DataSource.Exec("DROP TABLE IF EXISTS test_tracking_events")
	defer db.DataSource.Exec("DROP TABLE IF EXISTS test_tracking_events")

	schema := &db233.TrackingSchema{
		Version: "1",
		Tables: []db233.TrackingTable{
			{
				Name: "test_tracking_events",
				Columns: []db233.TrackingColumn{
					{Name: "player_id", Type: "string", Size: 64, PrimaryKey: true, Required: true},
					{Name: "event_time", Type: "timestamp", Required: true},
					{Name: "action", Type: "string", Size: 64, Required: true},
					{Name: "score", Type: "int"},
				},
				Indexes: []db233.TrackingIndex{
					{Name: "idx_test_tracking_action", Columns: []string{"action"}},
				},
			},
		},
	}

	plan, err := db233.ApplyTrackingSchema(db, schema, nil)
	if err != nil {
		t.Fatalf("同步埋点表失败: %v", err)
	}
	if len(plan.Statements) != 2 {
		t.Fatalf("首次同步应建表+索引，得到语句数: %d", len(plan.Statements))
	}

	table, _ := schema.GetTable("test_tracking_events")
	if _, err := db233.InsertTrackingPayload(db, table, map[string]any{
		"player_id":  "p001",
		"event_time": "2026-06-12 10:00:00",
		"action":     "level_up",
		"score":      100,
	}); err != nil {
		t.Fatalf("写入埋点失败: %v", err)
	}

	count := db.QueryToInt("SELECT COUNT(*) FROM test_tracking_events WHERE player_id = ?", "p001")
	if count != 1 {
		t.Fatalf("埋点写入数量错误: %d", count)
	}

	schema.Tables[0].Columns = append(schema.Tables[0].Columns, db233.TrackingColumn{Name: "extra", Type: "json"})
	plan, err = db233.ApplyTrackingSchema(db, schema, nil)
	if err != nil {
		t.Fatalf("补列失败: %v", err)
	}
	if len(plan.Statements) != 1 || plan.Statements[0].Operation != "add_column" {
		t.Fatalf("第二次同步应只补列，plan=%+v", plan)
	}
}

func TestTrackingSchemaFileLocalCacheSkipsUnchangedIntegration(t *testing.T) {
	db := CreateTestDb(t)
	if db == nil {
		return
	}
	defer db.Close()

	db.DataSource.Exec("DROP TABLE IF EXISTS test_tracking_cache_events")
	defer db.DataSource.Exec("DROP TABLE IF EXISTS test_tracking_cache_events")

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "tracking-schema.json")
	cachePath := filepath.Join(dir, "tracking-schema-cache.json")
	writeSchema := func(extraColumn bool) {
		extra := ""
		if extraColumn {
			extra = `,
        {"name": "extra", "type": "json"}`
		}
		content := `{
  "version": "1",
  "tables": [
    {
      "name": "test_tracking_cache_events",
      "columns": [
        {"name": "player_id", "type": "string", "size": 64, "primaryKey": true, "required": true},
        {"name": "action", "type": "string", "size": 64, "required": true}` + extra + `
      ]
    }
  ]
}`
		if err := os.WriteFile(schemaPath, []byte(content), 0644); err != nil {
			t.Fatalf("写入 schema 失败: %v", err)
		}
	}
	writeSchema(false)

	opts := &db233.TrackingSchemaApplyOptions{CachePath: cachePath}
	_, plan, err := db233.ApplyTrackingSchemaFile(db, schemaPath, opts)
	if err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}
	if len(plan.Statements) != 1 || !plan.Changed {
		t.Fatalf("首次同步应建表，plan=%+v", plan)
	}
	if _, err := db233.LoadTrackingSchemaLocalCache(cachePath); err != nil {
		t.Fatalf("本地 cache 应已写入: %v", err)
	}

	_, plan, err = db233.ApplyTrackingSchemaFile(db, schemaPath, opts)
	if err != nil {
		t.Fatalf("第二次同步失败: %v", err)
	}
	if plan.Changed || len(plan.Statements) != 0 {
		t.Fatalf("文件未变应跳过同步，plan=%+v", plan)
	}

	writeSchema(true)
	_, plan, err = db233.ApplyTrackingSchemaFile(db, schemaPath, opts)
	if err != nil {
		t.Fatalf("schema 变更后同步失败: %v", err)
	}
	if !plan.Changed || len(plan.Statements) != 1 || plan.Statements[0].Operation != "add_column" {
		t.Fatalf("schema 变更后应只补列，plan=%+v", plan)
	}
}
