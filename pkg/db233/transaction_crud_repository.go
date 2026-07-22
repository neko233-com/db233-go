package db233

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
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

func (r *transactionCrudRepository) lockActiveTransactionContext(
	ctx context.Context,
) (*sql.Tx, context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, nil, NewValidationException("context 不能为 nil")
	}
	tx, unlock, err := r.lockActiveTransaction()
	if err != nil {
		return nil, nil, nil, err
	}
	operationCtx, cleanup, _, ctxErr := mergeTransactionOperationContext(ctx, r.manager.txCtx)
	if ctxErr != nil {
		unlock()
		return nil, nil, nil, NewTransactionExceptionWithCause(ctxErr, "事务操作上下文已结束")
	}
	return tx, operationCtx, func() {
		cleanup()
		unlock()
	}, nil
}

func (r *transactionCrudRepository) withActiveTransactionContext(
	ctx context.Context,
	operation func(*sql.Tx, context.Context) error,
) error {
	tx, operationCtx, unlock, err := r.lockActiveTransactionContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return joinErrorWithContext(operation(tx, operationCtx), operationCtx)
}

func (r *transactionCrudRepository) FindByIdContext(ctx context.Context, id any, entityType IDbEntity) (IDbEntity, error) {
	var entity IDbEntity
	err := r.withActiveTransactionContext(ctx, func(tx *sql.Tx, operationCtx context.Context) error {
		var loadErr error
		entity, loadErr = loadByIdStrictContext(operationCtx, tx, r.base, id, entityType)
		return loadErr
	})
	if err != nil || entity == nil {
		return nil, err
	}
	// Rows 已消费并关闭，先释放事务锁再执行用户 hook，允许 hook 安全重入。
	if err := runEntityDeserializeHook(entity); err != nil {
		return nil, err
	}
	return entity, nil
}

func (r *transactionCrudRepository) FindByIdsContext(ctx context.Context, ids []any, entityType IDbEntity) ([]IDbEntity, error) {
	var entities []IDbEntity
	err := r.withActiveTransactionContext(ctx, func(tx *sql.Tx, operationCtx context.Context) error {
		var loadErr error
		entities, loadErr = loadByIdsStrictContext(operationCtx, tx, r.base, ids, entityType)
		return loadErr
	})
	if err != nil {
		return nil, err
	}
	if err := deserializeStrictEntities(entities); err != nil {
		return nil, err
	}
	return entities, nil
}

func (r *transactionCrudRepository) FindAllContext(ctx context.Context, entityType IDbEntity) ([]IDbEntity, error) {
	var entities []IDbEntity
	err := r.withActiveTransactionContext(ctx, func(tx *sql.Tx, operationCtx context.Context) error {
		var loadErr error
		entities, loadErr = loadAllStrictContext(operationCtx, tx, r.base, entityType)
		return loadErr
	})
	if err != nil {
		return nil, err
	}
	if err := deserializeStrictEntities(entities); err != nil {
		return nil, err
	}
	return entities, nil
}

func (r *transactionCrudRepository) FindByConditionContext(
	ctx context.Context,
	condition string,
	params []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	var entities []IDbEntity
	err := r.withActiveTransactionContext(ctx, func(tx *sql.Tx, operationCtx context.Context) error {
		var loadErr error
		entities, loadErr = loadByConditionStrictContext(operationCtx, tx, r.base, condition, params, entityType)
		return loadErr
	})
	if err != nil {
		return nil, err
	}
	if err := deserializeStrictEntities(entities); err != nil {
		return nil, err
	}
	return entities, nil
}

func (r *transactionCrudRepository) SaveContext(ctx context.Context, entity IDbEntity) error {
	if ctx == nil {
		return NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return NewTransactionExceptionWithCause(ctxErr, "保存实体时上下文已结束")
	}
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	if err := r.validateEntitiesBeforeBuild([]IDbEntity{entity}); err != nil {
		return err
	}

	// 用户序列化 hook 和纯 SQL 计划构建不持有事务锁，允许 hook 安全重入；
	// 真正执行前会再次校验句柄仍绑定同一笔活跃事务。
	statement, err := r.base.buildBatchUpsertStatement([]IDbEntity{entity})
	if err != nil {
		return err
	}

	tx, operationCtx, unlock, err := r.lockActiveTransactionContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	reservationKeys, err := r.prepareAutoIncrementStatement(operationCtx, tx, statement)
	if err != nil {
		return err
	}
	result, err := r.base.executeBatchUpsertStatement(operationCtx, tx.ExecContext, statement)
	if err != nil {
		return NewQueryExceptionWithCause(
			joinErrorWithContext(err, operationCtx),
			fmt.Sprintf("事务内保存实体到表 %s 失败", statement.tableName),
		)
	}
	r.manager.reservePendingAutoIncrementKeys(reservationKeys)
	if assignIDs, idErr := r.base.batchAutoIncrementAction(statement, result); idErr != nil {
		return NewQueryExceptionWithCause(idErr, fmt.Sprintf("获取表 %s 的自增主键失败", statement.tableName))
	} else if assignIDs != nil {
		r.manager.postCommitActions = append(r.manager.postCommitActions, assignIDs)
	}
	return nil
}

func (r *transactionCrudRepository) SaveBatchUpsertContext(ctx context.Context, entities []IDbEntity) error {
	if r == nil || r.base == nil {
		return NewTransactionException("事务 Repository 未初始化")
	}
	if ctx == nil {
		return NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return NewTransactionExceptionWithCause(ctxErr, "批量保存实体时上下文已结束")
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
	if duplicateErr := validateUniqueTransactionEntityInstances(validEntities); duplicateErr != nil {
		return duplicateErr
	}
	if shapeErr := validateRepositoryBatchShapes(validEntities, r.base.getTableName); shapeErr != nil {
		return shapeErr
	}
	if err := r.validateEntitiesBeforeBuild(validEntities); err != nil {
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

	tx, operationCtx, unlock, err := r.lockActiveTransactionContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	reservationKeys := make([][]uintptr, len(statements))
	for i, statement := range statements {
		keys, prepareErr := r.prepareAutoIncrementStatement(operationCtx, tx, statement)
		if prepareErr != nil {
			return prepareErr
		}
		reservationKeys[i] = keys
	}

	for i, statement := range statements {
		result, execErr := r.base.executeBatchUpsertStatement(operationCtx, tx.ExecContext, statement)
		if execErr != nil {
			return NewQueryExceptionWithCause(
				joinErrorWithContext(execErr, operationCtx),
				fmt.Sprintf("事务内批量 UPSERT 到表 %s 失败", statement.tableName),
			)
		}
		r.manager.reservePendingAutoIncrementKeys(reservationKeys[i])
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
	if r == nil || r.base == nil {
		return 0, NewTransactionException("事务 Repository 未初始化")
	}
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
	if identifierErr := validateRepositorySQLIdentifiers(tableName, uidColumn, nil); identifierErr != nil {
		return 0, identifierErr
	}

	return r.executeDeleteContext(ctx, "DELETE FROM "+tableName+" WHERE "+uidColumn+" = ?", []any{id}, tableName)
}

func (r *transactionCrudRepository) DeleteByConditionContext(
	ctx context.Context,
	condition string,
	params []any,
	entityType IDbEntity,
) (int64, error) {
	if r == nil || r.base == nil {
		return 0, NewTransactionException("事务 Repository 未初始化")
	}
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
	if identifierErr := validateRepositoryTableIdentifier(tableName); identifierErr != nil {
		return 0, identifierErr
	}
	return r.executeDeleteContext(ctx, "DELETE FROM "+tableName+" WHERE "+condition, params, tableName)
}

func (r *transactionCrudRepository) executeDeleteContext(
	ctx context.Context,
	query string,
	params []any,
	tableName string,
) (int64, error) {
	tx, operationCtx, unlock, err := r.lockActiveTransactionContext(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock()

	result, err := tx.ExecContext(operationCtx, query, params...)
	if err != nil {
		return 0, NewQueryExceptionWithCause(
			joinErrorWithContext(err, operationCtx),
			fmt.Sprintf("事务内删除表 %s 的记录失败", tableName),
		)
	}
	affectedRows, err := result.RowsAffected()
	if err != nil {
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("获取表 %s 删除影响行数失败", tableName))
	}
	return affectedRows, nil
}

func (r *transactionCrudRepository) prepareAutoIncrementStatement(
	ctx context.Context,
	tx *sql.Tx,
	statement *batchUpsertStatement,
) ([]uintptr, error) {
	if statement == nil || !statement.assignAutoIncrement {
		return nil, nil
	}

	keys := make([]uintptr, 0, len(statement.entities))
	for _, entity := range statement.entities {
		key, ok := transactionEntityIdentity(entity)
		if !ok {
			return nil, NewValidationException(fmt.Sprintf(
				"事务内自增主键回填要求实体为非 nil *struct，实际类型: %T",
				entity,
			))
		}
		if _, exists := r.manager.pendingAutoIncrementKeys[key]; exists {
			return nil, NewValidationException("同一自增实体在主键回填前不能重复保存")
		}
		keys = append(keys, key)
	}

	statement.autoIncrementStep = 1
	if len(statement.entities) <= 1 {
		return keys, nil
	}
	step, err := r.manager.loadAutoIncrementStep(ctx, tx)
	if err != nil {
		return nil, err
	}
	statement.autoIncrementStep = step
	return keys, nil
}

func (tm *TransactionManager) loadAutoIncrementStep(ctx context.Context, tx *sql.Tx) (int64, error) {
	if tm.autoIncrementStepLoaded {
		return tm.autoIncrementStep, nil
	}
	if tm.db == nil || tm.db.DatabaseType != EnumDatabaseTypeMySQL {
		return 0, NewValidationException("批量自增主键回填当前仅支持 MySQL")
	}
	var step int64
	if err := tx.QueryRowContext(ctx, "SELECT @@SESSION.auto_increment_increment").Scan(&step); err != nil {
		return 0, NewQueryExceptionWithCause(
			joinErrorWithContext(err, ctx),
			"读取 MySQL auto_increment_increment 失败",
		)
	}
	if step <= 0 {
		return 0, NewQueryException(fmt.Sprintf("MySQL auto_increment_increment 非法: %d", step))
	}
	tm.autoIncrementStep = step
	tm.autoIncrementStepLoaded = true
	return step, nil
}

func (tm *TransactionManager) reservePendingAutoIncrementKeys(keys []uintptr) {
	if len(keys) == 0 {
		return
	}
	if tm.pendingAutoIncrementKeys == nil {
		tm.pendingAutoIncrementKeys = make(map[uintptr]struct{}, len(keys))
	}
	for _, key := range keys {
		tm.pendingAutoIncrementKeys[key] = struct{}{}
		tm.pendingAutoIncrementOrder = append(tm.pendingAutoIncrementOrder, key)
	}
}

func validateUniqueTransactionEntityInstances(entities []IDbEntity) error {
	seen := make(map[uintptr]int, len(entities))
	for index, entity := range entities {
		key, ok := transactionEntityIdentity(entity)
		if !ok {
			continue
		}
		if firstIndex, exists := seen[key]; exists {
			return NewValidationException(fmt.Sprintf(
				"事务批量保存包含重复实体实例: first_index=%d, duplicate_index=%d",
				firstIndex,
				index,
			))
		}
		seen[key] = index
	}
	return nil
}

func transactionEntityIdentity(entity IDbEntity) (uintptr, bool) {
	if isNilStrictValue(entity) {
		return 0, false
	}
	value := reflect.ValueOf(entity)
	if value.Kind() != reflect.Ptr || value.Elem().Kind() != reflect.Struct {
		return 0, false
	}
	return value.Pointer(), true
}

// validateEntitiesBeforeBuild 在运行用户序列化 hook 前串行检查事务身份和待回填实体。
// 真正执行 SQL 前仍会二次校验，覆盖检查结束后的 Commit/Rollback 竞态。
func (r *transactionCrudRepository) validateEntitiesBeforeBuild(entities []IDbEntity) error {
	if r == nil || r.manager == nil || r.tx == nil || r.base == nil {
		return NewTransactionException("事务 Repository 未初始化")
	}
	r.manager.operationMu.Lock()
	defer r.manager.operationMu.Unlock()
	r.manager.mu.RLock()
	defer r.manager.mu.RUnlock()

	if !r.manager.isActive || r.manager.tx == nil || r.manager.tx != r.tx {
		return NewTransactionException("事务 Repository 已失效")
	}
	for _, entity := range entities {
		key, ok := transactionEntityIdentity(entity)
		if !ok {
			continue
		}
		if _, exists := r.manager.pendingAutoIncrementKeys[key]; exists {
			return NewValidationException("同一自增实体在主键回填前不能重复保存")
		}
	}
	return nil
}
