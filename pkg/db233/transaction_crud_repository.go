package db233

import (
	"context"
	"database/sql"
	"fmt"
)

// TransactionCrudRepository 是绑定到一笔具体事务的窄 Entity Repository。
// 它不暴露底层连接、WAL、WriteBuffer、并发查询或 DB Statement 缓存。
type TransactionCrudRepository interface {
	StrictEntityRepository

	SaveContext(ctx context.Context, entity IDbEntity) error
	SaveBatchUpsertContext(ctx context.Context, entities []IDbEntity) error
	DeleteByIdContext(ctx context.Context, id any, entityType IDbEntity) (int64, error)
	DeleteByConditionContext(ctx context.Context, condition string, params []any, entityType IDbEntity) (int64, error)
}

type transactionCrudRepository struct {
	manager *TransactionManager
	tx      *sql.Tx
	base    *BaseCrudRepository
}

var _ TransactionCrudRepository = (*transactionCrudRepository)(nil)

// CrudRepository 返回绑定当前事务的 Repository。
// 返回的句柄捕获当前 *sql.Tx；事务结束后永久失效，不会绑定 manager 的下一笔事务。
func (tm *TransactionManager) CrudRepository() (TransactionCrudRepository, error) {
	if tm == nil {
		return nil, NewTransactionException("事务管理器不能为 nil")
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if !tm.isActive || tm.tx == nil {
		return nil, NewTransactionException("没有活跃的事务")
	}

	return &transactionCrudRepository{
		manager: tm,
		tx:      tm.tx,
		// 只复用 Entity 元数据与纯 SQL 构造 helper，不绑定普通 Repository 的 WAL。
		base: &BaseCrudRepository{db: tm.db},
	}, nil
}

// lockActiveTransaction 在整个 Repository 操作期间持有 manager 读锁，
// 防止 Commit/Rollback 与仍在消费的 Rows 或分块写入交错。
func (r *transactionCrudRepository) lockActiveTransaction() (*sql.Tx, func(), error) {
	if r == nil || r.manager == nil || r.tx == nil || r.base == nil {
		return nil, nil, NewTransactionException("事务 Repository 未初始化")
	}

	r.manager.operationMu.Lock()
	r.manager.mu.RLock()
	if !r.manager.isActive || r.manager.tx == nil || r.manager.tx != r.tx {
		r.manager.mu.RUnlock()
		r.manager.operationMu.Unlock()
		return nil, nil, NewTransactionException("事务 Repository 已失效")
	}
	return r.tx, func() {
		r.manager.mu.RUnlock()
		r.manager.operationMu.Unlock()
	}, nil
}

// validateActiveTransaction 在执行用户 hook 前做一次快速 identity 校验。
// hook 返回后仍由 lockActiveTransaction 再次校验，覆盖期间发生的 Commit/Rollback。
func (r *transactionCrudRepository) validateActiveTransaction() error {
	if r == nil || r.manager == nil || r.tx == nil || r.base == nil {
		return NewTransactionException("事务 Repository 未初始化")
	}
	r.manager.mu.RLock()
	defer r.manager.mu.RUnlock()
	if !r.manager.isActive || r.manager.tx == nil || r.manager.tx != r.tx {
		return NewTransactionException("事务 Repository 已失效")
	}
	return nil
}

func (r *transactionCrudRepository) withActiveTransaction(operation func(*sql.Tx) error) error {
	tx, unlock, err := r.lockActiveTransaction()
	if err != nil {
		return err
	}
	defer unlock()
	return operation(tx)
}

func (r *transactionCrudRepository) FindByIdContext(ctx context.Context, id any, entityType IDbEntity) (IDbEntity, error) {
	var entity IDbEntity
	err := r.withActiveTransaction(func(tx *sql.Tx) error {
		var loadErr error
		entity, loadErr = loadByIdStrictContext(ctx, tx, r.base, id, entityType)
		return loadErr
	})
	if err != nil || entity == nil {
		return nil, err
	}
	// Rows 已消费并关闭，先释放事务锁再执行用户 hook，允许 hook 安全重入。
	entity.DeserializeAfterLoadDb()
	return entity, nil
}

func (r *transactionCrudRepository) FindByIdsContext(ctx context.Context, ids []any, entityType IDbEntity) ([]IDbEntity, error) {
	var entities []IDbEntity
	err := r.withActiveTransaction(func(tx *sql.Tx) error {
		var loadErr error
		entities, loadErr = loadByIdsStrictContext(ctx, tx, r.base, ids, entityType)
		return loadErr
	})
	if err != nil {
		return nil, err
	}
	deserializeStrictEntities(entities)
	return entities, nil
}

func (r *transactionCrudRepository) FindAllContext(ctx context.Context, entityType IDbEntity) ([]IDbEntity, error) {
	var entities []IDbEntity
	err := r.withActiveTransaction(func(tx *sql.Tx) error {
		var loadErr error
		entities, loadErr = loadAllStrictContext(ctx, tx, r.base, entityType)
		return loadErr
	})
	if err != nil {
		return nil, err
	}
	deserializeStrictEntities(entities)
	return entities, nil
}

func (r *transactionCrudRepository) FindByConditionContext(
	ctx context.Context,
	condition string,
	params []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	var entities []IDbEntity
	err := r.withActiveTransaction(func(tx *sql.Tx) error {
		var loadErr error
		entities, loadErr = loadByConditionStrictContext(ctx, tx, r.base, condition, params, entityType)
		return loadErr
	})
	if err != nil {
		return nil, err
	}
	deserializeStrictEntities(entities)
	return entities, nil
}

func (r *transactionCrudRepository) SaveContext(ctx context.Context, entity IDbEntity) error {
	if ctx == nil {
		return NewValidationException("context 不能为 nil")
	}
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	if err := r.validateActiveTransaction(); err != nil {
		return err
	}

	// 用户序列化 hook 和纯 SQL 计划构建不持有事务锁，允许 hook 安全重入；
	// 真正执行前会再次校验句柄仍绑定同一笔活跃事务。
	statement, err := r.base.buildBatchUpsertStatement([]IDbEntity{entity})
	if err != nil {
		return err
	}

	tx, unlock, err := r.lockActiveTransaction()
	if err != nil {
		return err
	}
	defer unlock()

	result, err := r.base.executeBatchUpsertStatement(ctx, tx.ExecContext, statement)
	if err != nil {
		return NewQueryExceptionWithCause(err, fmt.Sprintf("事务内保存实体到表 %s 失败", statement.tableName))
	}
	if assignIDs, idErr := r.base.batchAutoIncrementAction(statement, result); idErr != nil {
		return NewQueryExceptionWithCause(idErr, fmt.Sprintf("获取表 %s 的自增主键失败", statement.tableName))
	} else if assignIDs != nil {
		r.manager.postCommitActions = append(r.manager.postCommitActions, assignIDs)
	}
	return nil
}

func (r *transactionCrudRepository) SaveBatchUpsertContext(ctx context.Context, entities []IDbEntity) error {
	if ctx == nil {
		return NewValidationException("context 不能为 nil")
	}
	if entities == nil {
		return NewValidationException("实体列表不能为 nil")
	}
	if len(entities) == 0 {
		return NewValidationException("实体列表不能为空")
	}

	validEntities := make([]IDbEntity, 0, len(entities))
	for i, entity := range entities {
		if isNilStrictValue(entity) {
			LogWarn("事务批量 UPSERT 跳过 nil 实体: 索引=%d", i)
			continue
		}
		validEntities = append(validEntities, entity)
	}
	if len(validEntities) == 0 {
		return NewValidationException("没有有效的实体可保存")
	}
	if err := r.validateActiveTransaction(); err != nil {
		return err
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultCrudPerformanceSettings().BatchUpsertChunkSize
	}
	statements := make([]*batchUpsertStatement, 0)
	for _, group := range groupEntitiesByTable(validEntities, r.base.getTableName) {
		for start := 0; start < len(group); start += chunkSize {
			end := start + chunkSize
			if end > len(group) {
				end = len(group)
			}
			statement, buildErr := r.base.buildBatchUpsertStatement(group[start:end])
			if buildErr != nil {
				return buildErr
			}
			statements = append(statements, statement)
		}
	}

	tx, unlock, err := r.lockActiveTransaction()
	if err != nil {
		return err
	}
	defer unlock()

	for _, statement := range statements {
		result, execErr := r.base.executeBatchUpsertStatement(ctx, tx.ExecContext, statement)
		if execErr != nil {
			return NewQueryExceptionWithCause(execErr, fmt.Sprintf("事务内批量 UPSERT 到表 %s 失败", statement.tableName))
		}
		if assignIDs, idErr := r.base.batchAutoIncrementAction(statement, result); idErr != nil {
			return NewQueryExceptionWithCause(idErr, fmt.Sprintf("获取表 %s 的自增主键失败", statement.tableName))
		} else if assignIDs != nil {
			r.manager.postCommitActions = append(r.manager.postCommitActions, assignIDs)
		}
	}
	return nil
}

func (r *transactionCrudRepository) DeleteByIdContext(
	ctx context.Context,
	id any,
	entityType IDbEntity,
) (int64, error) {
	if ctx == nil {
		return 0, NewValidationException("context 不能为 nil")
	}
	if isNilStrictValue(entityType) {
		return 0, NewValidationException("实体类型不能为 nil")
	}
	if id == nil {
		return 0, NewValidationException("删除ID不能为 nil")
	}

	tableName := r.base.getTableName(entityType)
	if tableName == "" {
		return 0, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}
	uidColumn := GetCrudManagerInstance().GetPrimaryKeyColumnName(entityType)
	if uidColumn == "" {
		uidColumn = "id"
	}

	return r.executeDeleteContext(ctx, "DELETE FROM "+tableName+" WHERE "+uidColumn+" = ?", []any{id}, tableName)
}

func (r *transactionCrudRepository) DeleteByConditionContext(
	ctx context.Context,
	condition string,
	params []any,
	entityType IDbEntity,
) (int64, error) {
	if ctx == nil {
		return 0, NewValidationException("context 不能为 nil")
	}
	if isNilStrictValue(entityType) {
		return 0, NewValidationException("实体类型不能为 nil")
	}
	if condition == "" {
		return 0, NewValidationException("删除条件不能为空")
	}

	tableName := r.base.getTableName(entityType)
	if tableName == "" {
		return 0, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}
	return r.executeDeleteContext(ctx, "DELETE FROM "+tableName+" WHERE "+condition, params, tableName)
}

func (r *transactionCrudRepository) executeDeleteContext(
	ctx context.Context,
	query string,
	params []any,
	tableName string,
) (int64, error) {
	tx, unlock, err := r.lockActiveTransaction()
	if err != nil {
		return 0, err
	}
	defer unlock()

	result, err := tx.ExecContext(ctx, query, params...)
	if err != nil {
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("事务内删除表 %s 的记录失败", tableName))
	}
	affectedRows, err := result.RowsAffected()
	if err != nil {
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("获取表 %s 删除影响行数失败", tableName))
	}
	return affectedRows, nil
}
