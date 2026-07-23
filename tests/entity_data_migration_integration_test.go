package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

const (
	entityMigrationIntegrationTable     = "test_entity_data_migration"
	entityMigrationIntegrationNamespace = "db233_integration"
	entityLifecycleIntegrationTable     = "test_entity_lifecycle"
	entityLifecycleIntegrationNamespace = "db233_lifecycle_integration"
)

type entityLifecycleV2 struct {
	ID      int64   `db:"id,not_null" primary_key:"true"`
	NewName *string `db:"new_name"`
}

func (*entityLifecycleV2) TableName() string       { return entityLifecycleIntegrationTable }
func (*entityLifecycleV2) SerializeBeforeSaveDb()  {}
func (*entityLifecycleV2) DeserializeAfterLoadDb() {}
func (*entityLifecycleV2) GetTableMetaData() *db233.TableMetaData {
	return &db233.TableMetaData{TableName: entityLifecycleIntegrationTable}
}

func TestEntityDataMigrationProductionMySQL(t *testing.T) {
	database := CreateTestDb(t)
	if database == nil {
		return
	}
	t.Cleanup(func() {
		_, _ = database.DataSource.Exec(
			"DELETE FROM db233_entity_migrations WHERE namespace = ?",
			entityMigrationIntegrationNamespace,
		)
		_, _ = database.DataSource.Exec("DROP TABLE IF EXISTS `" + entityMigrationIntegrationTable + "`")
		if closeErr := database.Close(); closeErr != nil {
			t.Errorf("关闭 Entity 迁移集成数据库失败: %v", closeErr)
		}
	})
	if _, err := database.DataSource.Exec("DROP TABLE IF EXISTS `" + entityMigrationIntegrationTable + "`"); err != nil {
		t.Fatalf("清理 Entity 迁移集成表失败: %v", err)
	}
	if _, err := database.DataSource.Exec(`
		CREATE TABLE test_entity_data_migration (
			id BIGINT NOT NULL PRIMARY KEY,
			old_name VARCHAR(64) NOT NULL,
			new_name VARCHAR(64) NULL,
			score INT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		t.Fatalf("创建 Entity 迁移集成表失败: %v", err)
	}
	if _, err := database.DataSource.Exec(`
		INSERT INTO test_entity_data_migration (id, old_name) VALUES (1, 'player-1')`); err != nil {
		t.Fatalf("写入 Entity 迁移集成数据失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrations := []db233.EntityDataMigration{{
		Scope:       entityMigrationIntegrationTable,
		Version:     1,
		Order:       2026072301,
		Name:        "copy_old_name_to_new_name",
		Fingerprint: "UPDATE new_name=old_name WHERE new_name IS NULL; verify-zero-mismatch",
		Up: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
			_, err := tx.ExecContext(ctx, `
				UPDATE test_entity_data_migration
				SET new_name = old_name
				WHERE new_name IS NULL`)
			return err
		},
		Verify: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
			var mismatchCount int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM test_entity_data_migration
				WHERE new_name IS NULL OR new_name <> old_name`).Scan(&mismatchCount); err != nil {
				return err
			}
			if mismatchCount != 0 {
				return errors.New("名称迁移校验失败")
			}
			return nil
		},
	}}
	report, err := database.ApplyEntityDataMigrations(ctx, migrations, &db233.EntityDataMigrationOptions{
		Namespace: entityMigrationIntegrationNamespace,
	})
	if err != nil || report.Applied != 1 || report.Pending != 0 {
		t.Fatalf("首次 Entity 数据迁移失败: report=%+v err=%v", report, err)
	}
	report, err = database.ApplyEntityDataMigrations(ctx, migrations, &db233.EntityDataMigrationOptions{
		Namespace: entityMigrationIntegrationNamespace,
	})
	if err != nil || report.Skipped != 1 || report.Applied != 0 {
		t.Fatalf("重复 Entity 数据迁移不幂等: report=%+v err=%v", report, err)
	}
	state, err := database.GetEntityMigrationState(ctx, entityMigrationIntegrationNamespace)
	if err != nil ||
		state.CurrentOrder != 2026072301 ||
		state.ScopeVersions[entityMigrationIntegrationTable] != 1 ||
		state.AppliedCount != 1 {
		t.Fatalf("Entity 数据库版本异常: state=%+v err=%v", state, err)
	}

	failing := append(migrations, db233.EntityDataMigration{
		Scope:       entityMigrationIntegrationTable,
		Version:     2,
		Order:       2026072302,
		Name:        "rollback_on_verify_failure",
		Fingerprint: "UPDATE score=99; verify-fails",
		Up: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
			_, updateErr := tx.ExecContext(ctx, "UPDATE test_entity_data_migration SET score = 99")
			return updateErr
		},
		Verify: func(context.Context, *db233.EntityDataMigrationTx) error {
			return errors.New("预期的校验失败")
		},
	})
	if _, err := database.ApplyEntityDataMigrations(ctx, failing, &db233.EntityDataMigrationOptions{
		Namespace: entityMigrationIntegrationNamespace,
	}); err == nil {
		t.Fatal("Verify 失败应回滚迁移")
	}
	var score int
	if err := database.DataSource.QueryRow(
		"SELECT score FROM test_entity_data_migration WHERE id = 1",
	).Scan(&score); err != nil || score != 0 {
		t.Fatalf("失败迁移未完整回滚: score=%d err=%v", score, err)
	}
}

func TestEntitySchemaLifecycleProductionMySQL(t *testing.T) {
	database := CreateTestDb(t)
	if database == nil {
		return
	}
	t.Cleanup(func() {
		_, _ = database.DataSource.Exec(
			"DELETE FROM db233_entity_migrations WHERE namespace = ?",
			entityLifecycleIntegrationNamespace,
		)
		_, _ = database.DataSource.Exec("DROP TABLE IF EXISTS `" + entityLifecycleIntegrationTable + "`")
		if closeErr := database.Close(); closeErr != nil {
			t.Errorf("关闭 Entity 生命周期集成数据库失败: %v", closeErr)
		}
	})
	if _, err := database.DataSource.Exec("DROP TABLE IF EXISTS `" + entityLifecycleIntegrationTable + "`"); err != nil {
		t.Fatalf("清理 Entity 生命周期集成表失败: %v", err)
	}
	if _, err := database.DataSource.Exec(`
		CREATE TABLE test_entity_lifecycle (
			id BIGINT NOT NULL PRIMARY KEY,
			old_name VARCHAR(255) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		t.Fatalf("创建 Entity 生命周期集成表失败: %v", err)
	}
	if _, err := database.DataSource.Exec(`
		INSERT INTO test_entity_lifecycle (id, old_name) VALUES (1, 'player-1')`); err != nil {
		t.Fatalf("写入 Entity 生命周期集成数据失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prePermissions := db233.DefaultSchemaMigrationPermissions()
	finalizePermissions := db233.DefaultSchemaMigrationPermissions()
	finalizePermissions.DeleteColumn = true
	report, err := database.AutoMigrateEntityLifecycle(ctx, []any{&entityLifecycleV2{}},
		&db233.EntitySchemaLifecycleOptions{
			Namespace:            entityLifecycleIntegrationNamespace,
			PreSchemaPermissions: &prePermissions,
			DataMigrations: []db233.EntityDataMigration{{
				Scope:       entityLifecycleIntegrationTable,
				Version:     1,
				Order:       2026072301,
				Name:        "copy_then_remove_old_name",
				Fingerprint: "copy old_name to new_name before finalize",
				Up: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
					_, updateErr := tx.ExecContext(ctx, `
						UPDATE test_entity_lifecycle
						SET new_name = old_name
						WHERE new_name IS NULL`)
					return updateErr
				},
				Verify: func(ctx context.Context, tx *db233.EntityDataMigrationTx) error {
					var mismatchCount int
					if scanErr := tx.QueryRowContext(ctx, `
						SELECT COUNT(*) FROM test_entity_lifecycle
						WHERE new_name IS NULL OR new_name <> old_name`).Scan(&mismatchCount); scanErr != nil {
						return scanErr
					}
					if mismatchCount != 0 {
						return errors.New("名称迁移校验失败")
					}
					return nil
				},
			}},
			FinalizePermissions: &finalizePermissions,
			RequireExact:        true,
		})
	if err != nil {
		t.Fatalf("Entity 生命周期迁移失败: report=%+v err=%v", report, err)
	}
	if report.Data.Applied != 1 ||
		report.Version.CurrentOrder != 2026072301 ||
		!report.FinalSchema.Exact {
		t.Fatalf("Entity 生命周期报告异常: %+v", report)
	}
	var oldColumnCount int
	if err := database.DataSource.QueryRow(`
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = ?
		  AND COLUMN_NAME = 'old_name'`,
		entityLifecycleIntegrationTable,
	).Scan(&oldColumnCount); err != nil || oldColumnCount != 0 {
		t.Fatalf("旧列未在 Finalize 阶段删除: count=%d err=%v", oldColumnCount, err)
	}
	var newName string
	if err := database.DataSource.QueryRow(
		"SELECT new_name FROM test_entity_lifecycle WHERE id = 1",
	).Scan(&newName); err != nil || newName != "player-1" {
		t.Fatalf("迁移数据异常: newName=%q err=%v", newName, err)
	}
}
