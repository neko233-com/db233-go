package db233

import (
	"context"
	"crypto/sha256"
	stdsql "database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const entitySchemaVersionTableName = "db233_entity_schema_versions"

// EntitySchemaVersionRecord 是单张 Entity 表的结构版本。
// Fingerprint 改变时 Version 自动递增；WAL 仅允许同版本回放。
type EntitySchemaVersionRecord struct {
	Namespace   string    `json:"namespace"`
	TableName   string    `json:"tableName"`
	Version     int64     `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type entitySchemaVersionState struct {
	mu       sync.RWMutex
	versions map[string]int64
}

func (db *Db) installEntitySchemaVersions(records []EntitySchemaVersionRecord) {
	if db == nil {
		return
	}
	state := db.entitySchemaVersions
	if state == nil {
		db.resourceMu.Lock()
		if db.entitySchemaVersions == nil {
			db.entitySchemaVersions = &entitySchemaVersionState{}
		}
		state = db.entitySchemaVersions
		db.resourceMu.Unlock()
	}
	versions := make(map[string]int64, len(records))
	for _, record := range records {
		versions[strings.ToLower(record.TableName)] = record.Version
	}
	state.mu.Lock()
	state.versions = versions
	state.mu.Unlock()
}

// EntitySchemaVersion 返回当前进程已确认的单表结构版本。
// 0 表示尚未通过 Entity 生命周期绑定，保留给历史恢复文件兼容。
func (db *Db) EntitySchemaVersion(tableName string) int64 {
	if db == nil {
		return 0
	}
	db.resourceMu.Lock()
	state := db.entitySchemaVersions
	db.resourceMu.Unlock()
	if state == nil {
		return 0
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.versions[strings.ToLower(tableName)]
}

// VerifyAndLoadEntitySchemaVersions 校验数据库中已记录的单表结构指纹并加载版本。
// 用于禁止 Auto DDL 的生产启动；缺记录或指纹变化均 fail-fast。
func (db *Db) VerifyAndLoadEntitySchemaVersions(
	ctx context.Context,
	namespace string,
	entities []any,
) ([]EntitySchemaVersionRecord, error) {
	if db == nil || db.DataSource == nil {
		return nil, NewQueryException("数据库连接未初始化")
	}
	if strings.TrimSpace(namespace) == "" {
		return nil, NewValidationException("Entity schema version namespace 不能为空")
	}
	specs, err := buildSchemaEntitySpecs(ctx, db, entities)
	if err != nil {
		return nil, err
	}
	fingerprints, err := buildEntitySchemaFingerprints(db, specs)
	if err != nil {
		return nil, err
	}
	conn, err := db.DataSource.Conn(ctx)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "获取 Entity schema version 连接失败")
	}
	defer conn.Close()
	records, err := readEntitySchemaVersions(ctx, conn, namespace)
	if err != nil {
		return nil, err
	}
	byTable := make(map[string]EntitySchemaVersionRecord, len(records))
	for _, record := range records {
		byTable[strings.ToLower(record.TableName)] = record
	}
	for tableName, fingerprint := range fingerprints {
		record, exists := byTable[strings.ToLower(tableName)]
		if !exists {
			return nil, NewValidationException(fmt.Sprintf("Entity 表缺少结构版本记录: Table=%s", safeValueForLog(tableName)))
		}
		if record.Fingerprint != fingerprint {
			return nil, NewValidationException(fmt.Sprintf("Entity 表结构版本指纹不一致: Table=%s, Version=%d", safeValueForLog(tableName), record.Version))
		}
	}
	db.installEntitySchemaVersions(records)
	return records, nil
}

func syncEntitySchemaVersions(
	ctx context.Context,
	conn *stdsql.Conn,
	namespace string,
	db *Db,
	entities []any,
) ([]EntitySchemaVersionRecord, error) {
	if err := ensureEntitySchemaVersionTable(ctx, conn); err != nil {
		return nil, err
	}
	specs, err := buildSchemaEntitySpecs(ctx, db, entities)
	if err != nil {
		return nil, err
	}
	fingerprints, err := buildEntitySchemaFingerprints(db, specs)
	if err != nil {
		return nil, err
	}
	current, err := readEntitySchemaVersions(ctx, conn, namespace)
	if err != nil {
		return nil, err
	}
	byTable := make(map[string]EntitySchemaVersionRecord, len(current))
	for _, record := range current {
		byTable[strings.ToLower(record.TableName)] = record
	}
	for tableName, fingerprint := range fingerprints {
		record, exists := byTable[strings.ToLower(tableName)]
		if !exists {
			record = EntitySchemaVersionRecord{Namespace: namespace, TableName: tableName, Version: 1}
		} else if record.Fingerprint != fingerprint {
			record.Version++
		}
		record.Fingerprint = fingerprint
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO db233_entity_schema_versions
				(namespace, table_name, schema_version, schema_fingerprint)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				schema_version = VALUES(schema_version),
				schema_fingerprint = VALUES(schema_fingerprint),
				updated_at = CURRENT_TIMESTAMP(6)`,
			namespace, tableName, record.Version, fingerprint,
		); err != nil {
			return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "更新 Entity 单表结构版本失败")
		}
	}
	records, err := readEntitySchemaVersions(ctx, conn, namespace)
	if err != nil {
		return nil, err
	}
	db.installEntitySchemaVersions(records)
	return records, nil
}

func ensureEntitySchemaVersionTable(ctx context.Context, conn *stdsql.Conn) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS db233_entity_schema_versions (
			namespace VARCHAR(128) NOT NULL,
			table_name VARCHAR(128) NOT NULL,
			schema_version BIGINT NOT NULL,
			schema_fingerprint CHAR(64) NOT NULL,
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (namespace, table_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	if err != nil {
		return NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "创建 Entity 单表结构版本表失败")
	}
	return nil
}

func readEntitySchemaVersions(
	ctx context.Context,
	conn *stdsql.Conn,
	namespace string,
) (records []EntitySchemaVersionRecord, resultErr error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT namespace, table_name, schema_version, schema_fingerprint, updated_at
		FROM db233_entity_schema_versions
		WHERE namespace = ?
		ORDER BY table_name`, namespace)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "读取 Entity 单表结构版本失败")
	}
	defer func() {
		resultErr = errorsJoinClose(resultErr, rows.Close(), "关闭 Entity 单表结构版本结果集失败")
	}()
	for rows.Next() {
		var record EntitySchemaVersionRecord
		if err := rows.Scan(&record.Namespace, &record.TableName, &record.Version, &record.Fingerprint, &record.UpdatedAt); err != nil {
			return nil, NewQueryExceptionWithCause(err, "扫描 Entity 单表结构版本失败")
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "遍历 Entity 单表结构版本失败")
	}
	return records, nil
}

func buildEntitySchemaFingerprints(db *Db, specs []schemaEntitySpec) (map[string]string, error) {
	strategy, err := contextSchemaStrategy(db)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(specs))
	for _, spec := range specs {
		parts := []string{"table=" + strings.ToLower(spec.tableName)}
		columnNames := make([]string, 0, len(spec.columns))
		for name := range spec.columns {
			columnNames = append(columnNames, name)
		}
		sort.Strings(columnNames)
		for _, name := range columnNames {
			parts = append(parts, "column="+strings.ToLower(name)+":"+schemaExpectedColumnSummary(strategy, spec, name, spec.columns[name]))
		}
		indexNames := make([]string, 0, len(spec.indexes))
		for name := range spec.indexes {
			indexNames = append(indexNames, name)
		}
		sort.Strings(indexNames)
		for _, name := range indexNames {
			parts = append(parts, "index="+strings.ToLower(name)+":"+schemaIndexSummary(spec.indexes[name]))
		}
		sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
		result[spec.tableName] = hex.EncodeToString(sum[:])
	}
	return result, nil
}

func errorsJoinClose(current, closeErr error, message string) error {
	if closeErr == nil {
		return current
	}
	return errors.Join(current, NewQueryExceptionWithCause(closeErr, message))
}
