package db233

import (
	"context"
	"errors"
	"fmt"
)

// WarmGameDb 冷启动预热的有界兼容入口。
// 需要自定义 deadline 或启动取消的调用方应使用 WarmGameDbContext。
func WarmGameDb(db *Db, entities []IDbEntity) error {
	// Preserve the historical no-op contract for the compatibility entrypoint.
	// WarmGameDbContext is the strict API and rejects an uninitialized Db.
	if db == nil || db.DataSource == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultWarmupTimeout)
	defer cancel()
	return WarmGameDbContext(ctx, db, entities)
}

// WarmGameDbContext 严格预热连接池、实体元数据、SQL 模板、Stmt 和 ORM 扫描计划。
// Ping/Prepare/Query 共用调用方 context；任一阶段失败均保留 cause 并立即停止。
func WarmGameDbContext(ctx context.Context, db *Db, entities []IDbEntity) error {
	if ctx == nil {
		return NewValidationException("冷启动预热 context 不能为 nil")
	}
	if db == nil || db.DataSource == nil {
		return NewValidationException("冷启动预热需要已初始化的 Db")
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if !settings.EnableColdStartWarmup {
		return ctx.Err()
	}
	_, releaseGeneration, generationErr := db.lockCurrentDatabaseGenerationContext(ctx)
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()

	rounds := settings.PoolWarmupRounds
	if rounds <= 0 {
		rounds = settings.MaxIdleConns
		if rounds <= 0 {
			rounds = 5
		}
		if rounds > 20 {
			rounds = 20
		}
	}
	if err := WarmConnectionPoolContext(ctx, db.DataSource, rounds); err != nil {
		return fmt.Errorf("连接池预热 Ping: %w", err)
	}

	cm := GetCrudManagerInstance()
	metaCache := GetEntityMetadataCacheInstance()
	planCache := GetOrmScanPlanCache()
	sqlCache := GetSqlTemplateCache()
	stmtCache := GetPreparedStmtCache()

	for index, entity := range entities {
		if err := ctx.Err(); err != nil {
			return err
		}
		if isNilStrictValue(entity) {
			return NewValidationException(fmt.Sprintf("预热实体不能为 nil: index=%d", index))
		}
		if err := warmGameEntityContext(ctx, db, entity, settings, cm, metaCache, planCache, sqlCache, stmtCache); err != nil {
			return fmt.Errorf("预热实体失败: index=%d, type=%T: %w", index, entity, err)
		}
	}

	LogInfo("冷启动预热完成: entities=%d, poolRounds=%d", len(entities), rounds)
	return nil
}

func warmGameEntityContext(
	ctx context.Context,
	db *Db,
	entity IDbEntity,
	settings CrudPerformanceSettings,
	cm *CrudManager,
	metaCache *EntityMetadataCache,
	planCache *OrmScanPlanCache,
	sqlCache *SqlTemplateCache,
	stmtCache *PreparedStmtCache,
) (resultErr error) {
	var rowsToClose interface{ Close() error }
	defer func() {
		if rowsToClose != nil {
			if closeErr := rowsToClose.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("关闭预热查询: %w", closeErr))
			}
		}
		if recovered := recover(); recovered != nil {
			panicErr := NewQueryException(fmt.Sprintf(
				"预热实体时 panic: entity=%T, value=%s",
				entity,
				safeValueForLog(recovered),
			))
			if cause, ok := recovered.(error); ok {
				panicErr = NewQueryExceptionWithCause(cause, fmt.Sprintf("预热实体时 panic: entity=%T", entity))
			}
			resultErr = errors.Join(resultErr, panicErr)
		}
	}()

	if _, err := metaCache.GetOrBuild(entity); err != nil {
		return fmt.Errorf("预热实体元数据: %w", err)
	}
	tableName := entity.TableName()
	if tableName == "" {
		return NewValidationException(fmt.Sprintf("预热实体表名为空: %T", entity))
	}
	pkCol := cm.GetPrimaryKeyColumnName(entity)
	if pkCol == "" {
		pkCol = "id"
	}
	if err := validateRepositorySQLIdentifiers(tableName, pkCol, nil); err != nil {
		return err
	}

	var findSQL string
	if settings.EnableSqlTemplateCache {
		findSQL = sqlCache.GetFindByIdSQL(entity, tableName, pkCol)
	} else {
		findSQL = "SELECT * FROM " + tableName + " WHERE " + pkCol + " = ?"
	}
	if settings.EnablePreparedStmtCache {
		_, release, err := stmtCache.acquireStmtContext(ctx, db.DataSource, findSQL)
		if err != nil {
			return fmt.Errorf("预热 Stmt: table=%s: %w", safeValueForLog(tableName), err)
		}
		release()
	}

	rows, err := db.DataSource.QueryContext(ctx, "SELECT * FROM "+tableName+" LIMIT 0")
	if err != nil {
		return fmt.Errorf("预热列元数据: table=%s: %w", safeValueForLog(tableName), err)
	}
	rowsToClose = rows
	columns, columnsErr := rows.Columns()
	rowsErrBeforeClose := rows.Err()
	closeErr := rows.Close()
	rowsToClose = nil
	rowsErrAfterClose := rows.Err()
	if queryErr := errors.Join(columnsErr, rowsErrBeforeClose, closeErr, rowsErrAfterClose); queryErr != nil {
		return fmt.Errorf("读取并关闭预热列: table=%s: %w", safeValueForLog(tableName), queryErr)
	}
	if len(columns) == 0 {
		return NewQueryException(fmt.Sprintf("预热查询未返回列: table=%s", safeValueForLog(tableName)))
	}
	if _, err := planCache.GetPlan(entity, columns); err != nil {
		return fmt.Errorf("预热 ORM 扫描计划: table=%s: %w", safeValueForLog(tableName), err)
	}
	return nil
}
