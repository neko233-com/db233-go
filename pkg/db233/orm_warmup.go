package db233

import (
	"fmt"
)

// WarmGameDb 冷启动预热：连接池、实体元数据、SQL 模板、Stmt 缓存、ORM 扫描计划。
func WarmGameDb(db *Db, entities []IDbEntity) error {
	if db == nil || db.DataSource == nil {
		return nil
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if !settings.EnableColdStartWarmup {
		return nil
	}

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
	if err := WarmConnectionPool(db.DataSource, rounds); err != nil {
		LogWarn("连接池预热 Ping 失败: %v", err)
	}

	cm := GetCrudManagerInstance()
	metaCache := GetEntityMetadataCacheInstance()
	planCache := GetOrmScanPlanCache()
	sqlCache := GetSqlTemplateCache()
	stmtCache := GetPreparedStmtCache()

	for _, entity := range entities {
		if entity == nil {
			continue
		}
		if _, err := metaCache.GetOrBuild(entity); err != nil {
			LogWarn("预热实体元数据失败: %T, err=%v", entity, err)
			continue
		}

		tableName := entity.TableName()
		if tableName == "" {
			continue
		}
		pkCol := cm.GetPrimaryKeyColumnName(entity)
		if pkCol == "" {
			pkCol = "id"
		}

		var sql string
		if settings.EnableSqlTemplateCache {
			sql = sqlCache.GetFindByIdSQL(entity, tableName, pkCol)
		} else {
			sql = "SELECT * FROM " + tableName + " WHERE " + pkCol + " = ?"
		}
		if settings.EnablePreparedStmtCache {
			if _, err := stmtCache.GetStmt(db.DataSource, sql); err != nil {
				LogDebug("预热 Stmt 失败: table=%s, err=%v", tableName, err)
			}
		}

		warmSQL := fmt.Sprintf("SELECT * FROM %s LIMIT 0", tableName)
		rows, err := db.DataSource.Query(warmSQL)
		if err != nil {
			LogDebug("预热列元数据失败: table=%s, err=%v", tableName, err)
			continue
		}
		columns, colErr := rows.Columns()
		_ = rows.Close()
		if colErr != nil || len(columns) == 0 {
			continue
		}
		if _, err := planCache.GetPlan(entity, columns); err != nil {
			LogDebug("预热 ORM 扫描计划失败: table=%s, err=%v", tableName, err)
		}
	}

	LogInfo("冷启动预热完成: entities=%d, poolRounds=%d", len(entities), rounds)
	return nil
}
