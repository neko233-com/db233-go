package db233

import (
	"context"
	"database/sql"
	"fmt"
)

// batchUpsertStatement 是普通 Repository 与事务 Repository 共享的批量写入计划。
// 它只包含同步 SQL 所需信息，不携带 WAL、WriteBuffer 或 Statement 缓存语义。
type batchUpsertStatement struct {
	query               string
	args                []any
	tableName           string
	uidColumn           string
	columns             []string
	entities            []IDbEntity
	assignAutoIncrement bool
}

type execContextFunc func(context.Context, string, ...any) (sql.Result, error)

// buildBatchUpsertStatement 复用现有 Entity 元数据、序列化钩子与 SQL 构造规则。
// 调用方负责按表分组和分块；本方法不会执行 SQL，也不会接入 WAL。
func (r *BaseCrudRepository) buildBatchUpsertStatement(validEntities []IDbEntity) (*batchUpsertStatement, error) {
	if len(validEntities) == 0 {
		return nil, nil
	}

	firstEntity := validEntities[0]
	tableName := r.getTableName(firstEntity)
	if tableName == "" {
		return nil, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	for i, entity := range validEntities {
		if entity == nil {
			return nil, NewValidationException(fmt.Sprintf("批量 UPSERT 实体不能为 nil: 索引=%d", i))
		}
		if r.getTableName(entity) != tableName {
			return nil, NewValidationException("单个批量 UPSERT 计划只能包含同一张表的实体")
		}
		entity.SerializeBeforeSaveDb()
	}

	firstFields := r.getFields(firstEntity)
	if len(firstFields) == 0 {
		return nil, NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段，请检查字段是否包含 db 标签", firstEntity))
	}

	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(firstEntity)
	if uidColumn == "" {
		uidColumn = "id"
	}
	isAutoIncrement := r.isAutoIncrementPrimaryKey(firstEntity, uidColumn)

	columns := make([]string, 0, len(firstFields))
	for name, value := range firstFields {
		if name == uidColumn && isAutoIncrement && r.isZeroValue(value) {
			continue
		}
		columns = append(columns, name)
	}
	if len(columns) == 0 {
		return nil, NewValidationException(fmt.Sprintf("表 %s 没有可插入的字段", tableName))
	}

	hasPrimaryKey := false
	for _, column := range columns {
		if column == uidColumn {
			hasPrimaryKey = true
			break
		}
	}
	if !hasPrimaryKey && !isAutoIncrement {
		return nil, NewValidationException(fmt.Sprintf("批量 UPSERT 要求主键 %s 有有效值", uidColumn))
	}
	if hasPrimaryKey {
		for _, entity := range validEntities {
			if r.isZeroValue(cm.GetPrimaryKeyValue(entity)) {
				return nil, NewValidationException(fmt.Sprintf("批量 UPSERT 要求所有实体主键 %s 非零值", uidColumn))
			}
		}
	}

	rowPlaceholder := "(" + joinQuestionMarks(len(columns)) + ")"
	placeholders := make([]string, 0, len(validEntities))
	allValues := make([]any, 0, len(validEntities)*len(columns))

	var fieldScratch map[string]any
	var batchScratch *batchUpsertScratch
	if EnableAllocPoolEnabled() {
		batchScratch = acquireBatchUpsertScratch()
		defer releaseBatchUpsertScratch(batchScratch)
		fieldScratch = batchScratch.fieldMap
	} else {
		fieldScratch = acquireFieldMap()
		defer releaseFieldMap(fieldScratch)
	}

	for _, entity := range validEntities {
		clear(fieldScratch)
		r.getFieldsInto(entity, fieldScratch)
		var rowValues []any
		if batchScratch != nil {
			batchScratch.rowValues = batchScratch.rowValues[:0]
			rowValues = batchScratch.rowValues
		} else {
			rowValues = make([]any, 0, len(columns))
		}
		for _, column := range columns {
			value, exists := fieldScratch[column]
			if !exists {
				value = r.getDefaultValueIfEmpty(nil, column)
			} else {
				value = r.getDefaultValueIfEmpty(value, column)
			}
			rowValues = append(rowValues, value)
		}
		placeholders = append(placeholders, rowPlaceholder)
		allValues = append(allValues, rowValues...)
	}

	var query string
	if !hasPrimaryKey && isAutoIncrement {
		if EnableAllocPoolEnabled() {
			query = appendBatchInsertSQL(tableName, columns, placeholders)
		} else {
			query = "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES " +
				StringUtilsInstance.Join(placeholders, ",")
		}
	} else {
		updateParts := make([]string, 0, len(columns))
		for _, column := range columns {
			if column != uidColumn {
				updateParts = append(updateParts, column+" = VALUES("+column+")")
			}
		}
		if EnableAllocPoolEnabled() {
			query = appendBatchUpsertSQL(tableName, columns, placeholders, updateParts)
		} else if len(updateParts) > 0 {
			query = "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES " +
				StringUtilsInstance.Join(placeholders, ",") + " ON DUPLICATE KEY UPDATE " + StringUtilsInstance.Join(updateParts, ", ")
		} else {
			query = "INSERT IGNORE INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES " +
				StringUtilsInstance.Join(placeholders, ",")
		}
	}

	return &batchUpsertStatement{
		query:               query,
		args:                allValues,
		tableName:           tableName,
		uidColumn:           uidColumn,
		columns:             columns,
		entities:            validEntities,
		assignAutoIncrement: !hasPrimaryKey && isAutoIncrement,
	}, nil
}

func (r *BaseCrudRepository) executeBatchUpsertStatement(
	ctx context.Context,
	exec execContextFunc,
	statement *batchUpsertStatement,
) (sql.Result, error) {
	if ctx == nil {
		return nil, NewValidationException("context 不能为 nil")
	}
	if exec == nil {
		return nil, NewValidationException("SQL executor 不能为 nil")
	}
	if statement == nil {
		return nil, nil
	}

	result, err := exec(ctx, statement.query, statement.args...)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// batchAutoIncrementAction 读取批量 INSERT 生成的首个 ID，并返回写回动作。
// 事务路径把动作延迟到 Commit 成功后执行，避免 Rollback 后留下不存在的主键。
func (r *BaseCrudRepository) batchAutoIncrementAction(
	statement *batchUpsertStatement,
	result sql.Result,
) (func(), error) {
	if statement == nil || !statement.assignAutoIncrement {
		return nil, nil
	}
	if result == nil {
		return nil, NewQueryException("批量 INSERT 未返回执行结果")
	}

	firstID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if firstID <= 0 {
		return nil, nil
	}

	return func() {
		for i, entity := range statement.entities {
			r.setPrimaryKeyValue(entity, firstID+int64(i))
		}
	}, nil
}
