package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neko233-com/db233-go/pkg/db233"
)

const schemaMigrationIntegrationTable = "test_schema_migration_production"

type schemaMigrationEntityV1 struct {
	ID   int64  `db:"id,not_null" primary_key:"true" auto_increment:"true"`
	Name string `db:"name,not_null" db_type:"varchar(64)"`
}

func (*schemaMigrationEntityV1) TableName() string       { return schemaMigrationIntegrationTable }
func (*schemaMigrationEntityV1) SerializeBeforeSaveDb()  {}
func (*schemaMigrationEntityV1) DeserializeAfterLoadDb() {}
func (*schemaMigrationEntityV1) GetTableMetaData() *db233.TableMetaData {
	return &db233.TableMetaData{
		TableName: schemaMigrationIntegrationTable,
		Indexes: []*db233.IndexMetaData{{
			IndexName: "idx_test_schema_name",
			Columns:   []string{"name"},
		}},
	}
}

type schemaMigrationEntityV2 struct {
	ID    int64  `db:"id,not_null" primary_key:"true" auto_increment:"true"`
	Name  string `db:"name,not_null" db_type:"varchar(64)"`
	Score *int   `db:"score"`
}

func (*schemaMigrationEntityV2) TableName() string       { return schemaMigrationIntegrationTable }
func (*schemaMigrationEntityV2) SerializeBeforeSaveDb()  {}
func (*schemaMigrationEntityV2) DeserializeAfterLoadDb() {}
func (*schemaMigrationEntityV2) GetTableMetaData() *db233.TableMetaData {
	return (&schemaMigrationEntityV1{}).GetTableMetaData()
}

func TestSchemaMigrationProductionMySQL(t *testing.T) {
	database := CreateTestDb(t)
	if database == nil {
		return
	}
	t.Cleanup(func() {
		if _, cleanupErr := database.DataSource.Exec("DROP TABLE IF EXISTS `" + schemaMigrationIntegrationTable + "`"); cleanupErr != nil {
			t.Errorf("清理 schema 集成表失败: %v", cleanupErr)
		}
		if closeErr := database.Close(); closeErr != nil {
			t.Errorf("关闭 schema 集成数据库失败: %v", closeErr)
		}
	})
	if _, err := database.DataSource.Exec("DROP TABLE IF EXISTS `" + schemaMigrationIntegrationTable + "`"); err != nil {
		t.Fatalf("清理 schema 集成表: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	beforeMetrics := database.FlushWriteMetrics()

	created, err := database.AutoMigrateSchema(ctx, []any{&schemaMigrationEntityV1{}}, nil)
	if err != nil {
		t.Fatalf("首次统一建表: %v, report=%+v", err, created)
	}
	if len(created.Tables) != 1 || created.Tables[0].Executed != 2 || !created.After.Compatible || !created.After.Exact {
		t.Fatalf("首次迁移报告异常: %+v", created)
	}
	if metrics := database.FlushWriteMetrics(); metrics.AttemptedSQL != beforeMetrics.AttemptedSQL {
		t.Fatalf("DDL 不应计入状态 flush 指标: before=%d after=%d", beforeMetrics.AttemptedSQL, metrics.AttemptedSQL)
	}

	idempotent, err := database.AutoMigrateSchema(ctx, []any{&schemaMigrationEntityV1{}}, nil)
	if err != nil {
		t.Fatalf("重复统一迁移: %v", err)
	}
	if len(idempotent.Tables) != 1 || len(idempotent.Tables[0].Actions) != 0 || idempotent.Tables[0].Executed != 0 {
		t.Fatalf("重复迁移不是幂等空计划: %+v", idempotent.Tables)
	}

	evolved, err := database.AutoMigrateSchema(ctx, []any{&schemaMigrationEntityV2{}}, nil)
	if err != nil {
		t.Fatalf("安全增列: %v, report=%+v", err, evolved)
	}
	if len(evolved.Tables) != 1 || evolved.Tables[0].Executed != 1 || !evolved.After.Exact {
		t.Fatalf("安全增列报告异常: %+v", evolved)
	}

	if _, err := database.DataSource.Exec(
		"ALTER TABLE `" + schemaMigrationIntegrationTable + "` ADD COLUMN `legacy` INT NULL",
	); err != nil {
		t.Fatalf("制造兼容 extra 漂移: %v", err)
	}
	compatible, err := database.VerifySchema(ctx, []any{&schemaMigrationEntityV2{}}, nil)
	if err != nil || !compatible.Compatible || compatible.Exact {
		t.Fatalf("兼容验证异常: report=%+v err=%v", compatible, err)
	}
	exact, err := database.VerifySchema(ctx, []any{&schemaMigrationEntityV2{}}, &db233.SchemaVerifyOptions{
		RequireExact: true,
	})
	if !errors.Is(err, db233.ErrSchemaVerificationFailed) || !exact.Compatible || exact.Exact {
		t.Fatalf("严格验证未识别 extra 漂移: report=%+v err=%v", exact, err)
	}

	if _, err := database.DataSource.Exec(
		"ALTER TABLE `" + schemaMigrationIntegrationTable + "` MODIFY COLUMN `name` VARCHAR(32) NOT NULL",
	); err != nil {
		t.Fatalf("制造类型漂移: %v", err)
	}
	blocked, err := database.AutoMigrateSchema(ctx, []any{&schemaMigrationEntityV2{}}, nil)
	if !errors.Is(err, db233.ErrSchemaVerificationFailed) || blocked.After.Compatible {
		t.Fatalf("安全默认未阻止改列: report=%+v err=%v", blocked, err)
	}
	permissions := db233.DefaultSchemaMigrationPermissions()
	permissions.UpdateColumn = true
	repaired, err := database.AutoMigrateSchema(ctx, []any{&schemaMigrationEntityV2{}}, &db233.SchemaMigrationOptions{
		Permissions: &permissions,
	})
	if err != nil || !repaired.After.Compatible {
		t.Fatalf("显式改列修复失败: report=%+v err=%v", repaired, err)
	}
}
