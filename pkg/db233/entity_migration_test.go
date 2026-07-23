package db233

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeEntityDataMigrationsSortsAndRejectsMutationHazards(t *testing.T) {
	migrate := func(_ context.Context, _ *EntityDataMigrationTx) error { return nil }
	migrations := []EntityDataMigration{
		{Scope: "Player", Version: 2, Order: 20, Name: "second", Fingerprint: "v1", Up: migrate},
		{Scope: "Player", Version: 1, Order: 10, Name: "first", Fingerprint: "v1", Up: migrate},
	}
	config, normalized, err := normalizeEntityDataMigrations(migrations, nil)
	if err != nil {
		t.Fatalf("规范化迁移失败: %v", err)
	}
	if config.namespace != "default" || normalized[0].Version != 1 || normalized[1].Version != 2 {
		t.Fatalf("规范化结果异常: config=%+v migrations=%+v", config, normalized)
	}

	duplicateOrder := append([]EntityDataMigration(nil), migrations...)
	duplicateOrder[1].Order = duplicateOrder[0].Order
	if _, _, err := normalizeEntityDataMigrations(duplicateOrder, nil); err == nil {
		t.Fatal("重复全局 Order 应被拒绝")
	}

	invalidScope := append([]EntityDataMigration(nil), migrations...)
	invalidScope[0].Scope = "Player Entity"
	if _, _, err := normalizeEntityDataMigrations(invalidScope, nil); err == nil {
		t.Fatal("含空格 Scope 应被拒绝")
	}
}

func TestBuildEntityMigrationPlanProtectsImmutableHistory(t *testing.T) {
	migrate := func(_ context.Context, _ *EntityDataMigrationTx) error { return nil }
	config, definitions, err := normalizeEntityDataMigrations([]EntityDataMigration{{
		Scope: "Player", Version: 1, Order: 10, Name: "copy_name", Fingerprint: "sql-v1", Up: migrate,
	}}, &EntityDataMigrationOptions{Namespace: "game"})
	if err != nil {
		t.Fatalf("规范化迁移失败: %v", err)
	}
	applied := []EntityDataMigrationRecord{{
		Namespace: "game",
		Scope:     "Player",
		Version:   1,
		Order:     10,
		Name:      "copy_name",
		Checksum:  definitions[0].checksum,
	}}
	report := EntityDataMigrationReport{Namespace: "game"}
	if err := buildEntityMigrationPlan(definitions, applied, config, &report); err != nil {
		t.Fatalf("已应用的相同迁移应幂等跳过: %v", err)
	}
	if report.Skipped != 1 || report.Pending != 0 {
		t.Fatalf("幂等计划异常: %+v", report)
	}

	applied[0].Checksum = "changed"
	report = EntityDataMigrationReport{Namespace: "game"}
	err = buildEntityMigrationPlan(definitions, applied, config, &report)
	if !errors.Is(err, ErrEntityMigrationDefinitionChanged) {
		t.Fatalf("篡改历史定义未被拒绝: %v", err)
	}
}

func TestEntityMigrationRejectsDDLAndBuildsVersionState(t *testing.T) {
	for _, statement := range []string{
		"",
		"ALTER TABLE player ADD COLUMN score INT",
		"DROP TABLE player",
		"COMMIT",
	} {
		if err := validateEntityMigrationDML(statement); err == nil {
			t.Fatalf("危险 SQL 应被拒绝: %q", statement)
		}
	}
	if err := validateEntityMigrationDML("UPDATE player SET score = ? WHERE id = ?"); err != nil {
		t.Fatalf("合法 DML 被拒绝: %v", err)
	}
	if err := validateEntityMigrationQuery("SELECT score FROM player WHERE id = ?"); err != nil {
		t.Fatalf("合法查询被拒绝: %v", err)
	}
	for _, statement := range []string{
		"UPDATE player SET score = 1",
		"WITH changed AS (SELECT 1) UPDATE player SET score = 1",
		"SELECT 1; DROP TABLE player",
	} {
		if err := validateEntityMigrationQuery(statement); err == nil {
			t.Fatalf("危险查询应被拒绝: %q", statement)
		}
	}

	firstAt := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Minute)
	state := buildEntityMigrationState("game", []EntityDataMigrationRecord{
		{Scope: "Player", Version: 1, Order: 10, AppliedAt: firstAt},
		{Scope: "Player", Version: 2, Order: 30, AppliedAt: secondAt},
		{Scope: "Guild", Version: 3, Order: 20, AppliedAt: firstAt},
	})
	if state.CurrentOrder != 30 ||
		state.AppliedCount != 3 ||
		state.ScopeVersions["Player"] != 2 ||
		state.ScopeVersions["Guild"] != 3 ||
		state.LastAppliedAt == nil ||
		!state.LastAppliedAt.Equal(secondAt) {
		t.Fatalf("版本快照异常: %+v", state)
	}
}
