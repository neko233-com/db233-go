package db233

import (
	"context"
	"fmt"
)

// EntitySchemaLifecycleOptions 控制“扩展 schema → 数据迁移 → 收缩 schema → 最终校验”。
// FinalizePermissions 为 nil 时默认不删除旧列/索引，适合跨版本渐进迁移。
type EntitySchemaLifecycleOptions struct {
	Namespace            string                      `json:"namespace"`
	MaxConcurrency       int                         `json:"maxConcurrency"`
	DryRun               bool                        `json:"dryRun"`
	LockTimeoutSeconds   int                         `json:"lockTimeoutSeconds"`
	PreSchemaPermissions *SchemaMigrationPermissions `json:"preSchemaPermissions,omitempty"`
	DataMigrations       []EntityDataMigration       `json:"-"`
	FinalizePermissions  *SchemaMigrationPermissions `json:"finalizePermissions,omitempty"`
	RequireExact         bool                        `json:"requireExact"`
	AllowUnknownApplied  bool                        `json:"allowUnknownApplied"`
}

// EntitySchemaLifecycleReport 汇总一次完整 Entity 生命周期编排。
type EntitySchemaLifecycleReport struct {
	DryRun      bool                      `json:"dryRun"`
	PreSchema   SchemaMigrationReport     `json:"preSchema"`
	Data        EntityDataMigrationReport `json:"data"`
	Finalize    *SchemaMigrationReport    `json:"finalize,omitempty"`
	FinalSchema SchemaVerificationReport  `json:"finalSchema"`
	Version     EntityMigrationState      `json:"version"`
}

// AutoMigrateEntityLifecycle 自动执行 Entity schema 与业务数据迁移的生产编排。
// PreSchema 禁止删列/删索引；业务迁移全部成功后才可能执行显式 FinalizePermissions。
func (db *Db) AutoMigrateEntityLifecycle(
	ctx context.Context,
	entities []any,
	options *EntitySchemaLifecycleOptions,
) (report EntitySchemaLifecycleReport, resultErr error) {
	if ctx == nil {
		return report, NewValidationException("context 不能为 nil")
	}
	config := EntitySchemaLifecycleOptions{}
	if options != nil {
		config = *options
	}
	report.DryRun = config.DryRun
	if db == nil || db.DataSource == nil {
		return report, NewQueryException("数据库连接未初始化")
	}
	if maxOpen := db.DataSource.Stats().MaxOpenConnections; maxOpen == 1 {
		return report, NewValidationException("Entity 生命周期编排至少需要 2 个数据库连接")
	}

	dataConfig, definitions, err := normalizeEntityDataMigrations(
		config.DataMigrations,
		&EntityDataMigrationOptions{
			Namespace:           config.Namespace,
			DryRun:              config.DryRun,
			LockTimeoutSeconds:  config.LockTimeoutSeconds,
			AllowUnknownApplied: config.AllowUnknownApplied,
		},
	)
	if err != nil {
		return report, err
	}
	report.Data = EntityDataMigrationReport{
		Namespace: dataConfig.namespace,
		DryRun:    dataConfig.dryRun,
		Steps:     make([]EntityDataMigrationStepReport, 0, len(definitions)),
	}
	report.Version = newEntityMigrationState(dataConfig.namespace)

	prePermissions := DefaultSchemaMigrationPermissions()
	if config.PreSchemaPermissions != nil {
		prePermissions = *config.PreSchemaPermissions
	}
	if prePermissions.DeleteColumn || prePermissions.DeleteIndex {
		return report, NewValidationException("PreSchema 阶段禁止删除列或索引；请使用 FinalizePermissions")
	}

	lockConn, releaseLock, err := acquireEntityMigrationAdvisoryLock(
		ctx,
		db,
		dataConfig.namespace,
		dataConfig.lockTimeoutSeconds,
	)
	if err != nil {
		return report, err
	}
	defer releaseLock()

	report.PreSchema, err = db.AutoMigrateSchema(ctx, entities, &SchemaMigrationOptions{
		MaxConcurrency: config.MaxConcurrency,
		DryRun:         config.DryRun,
		Permissions:    &prePermissions,
	})
	if err != nil {
		return report, fmt.Errorf("Entity PreSchema 迁移失败: %w", err)
	}

	releaseGeneration, err := lockSchemaDatabaseGeneration(ctx, db)
	if err != nil {
		return report, err
	}
	report.Data, err = db.applyEntityDataMigrationsOnConnection(
		ctx,
		lockConn,
		definitions,
		dataConfig,
		report.Data,
	)
	releaseGeneration()
	if err != nil {
		return report, fmt.Errorf("Entity DataMigration 失败: %w", err)
	}

	if config.FinalizePermissions != nil {
		finalizeReport, finalizeErr := db.AutoMigrateSchema(ctx, entities, &SchemaMigrationOptions{
			MaxConcurrency: config.MaxConcurrency,
			DryRun:         config.DryRun,
			Permissions:    config.FinalizePermissions,
		})
		report.Finalize = &finalizeReport
		if finalizeErr != nil {
			return report, fmt.Errorf("Entity FinalizeSchema 迁移失败: %w", finalizeErr)
		}
	}

	report.FinalSchema, err = db.VerifySchema(ctx, entities, &SchemaVerifyOptions{
		MaxConcurrency: config.MaxConcurrency,
		RequireExact:   config.RequireExact && !config.DryRun,
	})
	if err != nil {
		return report, fmt.Errorf("Entity FinalSchema 校验失败: %w", err)
	}
	exists, err := entityMigrationTableExists(ctx, lockConn)
	if err != nil {
		return report, fmt.Errorf("读取 Entity 数据库版本失败: %w", err)
	}
	if exists {
		records, readErr := readAppliedEntityMigrations(ctx, lockConn, dataConfig.namespace)
		if readErr != nil {
			return report, fmt.Errorf("读取 Entity 数据库版本失败: %w", readErr)
		}
		report.Version = buildEntityMigrationState(dataConfig.namespace, records)
	}
	return report, nil
}
