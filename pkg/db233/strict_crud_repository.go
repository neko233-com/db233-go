package db233

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
)

// StrictEntityRepository 提供 all-or-error 的 Entity 查询能力。
type StrictEntityRepository interface {
	FindByIdContext(ctx context.Context, id any, entityType IDbEntity) (IDbEntity, error)
	FindByIdsContext(ctx context.Context, ids []any, entityType IDbEntity) ([]IDbEntity, error)
	FindAllContext(ctx context.Context, entityType IDbEntity) ([]IDbEntity, error)
	FindByConditionContext(ctx context.Context, condition string, params []any, entityType IDbEntity) ([]IDbEntity, error)
}

// StrictCrudRepository 只组合严格读取与既有同步写入，不暴露 legacy 查询入口。
type StrictCrudRepository interface {
	StrictEntityRepository

	Save(entity IDbEntity) error
	SaveBatchUpsert(entities []IDbEntity) error
	DeleteById(id any, entityType IDbEntity) error
}

// strictRowsQueryer 是普通严格查询与事务严格查询共享的最小 runner。
// *sql.Tx 可直接实现该接口，事务路径无需暴露底层事务对象给公共 API。
type strictRowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type strictDBRowsQueryer struct {
	db *Db
}

func (q strictDBRowsQueryer) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if q.db == nil {
		return nil, NewQueryException("数据库未初始化")
	}
	return q.db.queryContext(ctx, query, args...)
}

// NewStrictCrudRepository 创建仅暴露严格读与同步写的窄 Repository。
func NewStrictCrudRepository(db *Db) StrictCrudRepository {
	return NewBaseCrudRepository(db)
}

func (r *BaseCrudRepository) FindByIdContext(ctx context.Context, id any, entityType IDbEntity) (IDbEntity, error) {
	if r == nil {
		return nil, NewValidationException("Repository 不能为 nil")
	}
	return findByIdStrictContext(ctx, strictDBRowsQueryer{db: r.db}, r, id, entityType)
}

func (r *BaseCrudRepository) FindByIdsContext(ctx context.Context, ids []any, entityType IDbEntity) ([]IDbEntity, error) {
	if r == nil {
		return nil, NewValidationException("Repository 不能为 nil")
	}
	return findByIdsStrictContext(ctx, strictDBRowsQueryer{db: r.db}, r, ids, entityType)
}

func (r *BaseCrudRepository) FindAllContext(ctx context.Context, entityType IDbEntity) ([]IDbEntity, error) {
	if r == nil {
		return nil, NewValidationException("Repository 不能为 nil")
	}
	return findAllStrictContext(ctx, strictDBRowsQueryer{db: r.db}, r, entityType)
}

func (r *BaseCrudRepository) FindByConditionContext(
	ctx context.Context,
	condition string,
	params []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	if r == nil {
		return nil, NewValidationException("Repository 不能为 nil")
	}
	return findByConditionStrictContext(ctx, strictDBRowsQueryer{db: r.db}, r, condition, params, entityType)
}

// executeQueryStrictContextWithRunner 对参数组逐一查询，任一组失败即丢弃全部结果。
// rows 的关闭由 ormBatchStrict 唯一负责。
func executeQueryStrictContextWithRunner(
	ctx context.Context,
	runner strictRowsQueryer,
	query string,
	paramsArray [][]any,
	returnType any,
) ([]any, error) {
	if ctx == nil {
		return nil, NewValidationException("context 不能为 nil")
	}
	if isNilStrictValue(runner) {
		return nil, NewValidationException("严格查询 runner 不能为 nil")
	}
	if _, typeErr := strictOrmStructType(returnType); typeErr != nil {
		return nil, typeErr
	}

	effectiveParams := paramsArray
	if len(effectiveParams) == 0 {
		effectiveParams = [][]any{{}}
	}

	results := make([]any, 0)
	for groupIndex, params := range effectiveParams {
		rows, queryErr := runner.QueryContext(ctx, query, params...)
		if queryErr != nil {
			return nil, NewQueryExceptionWithCause(
				queryErr,
				fmt.Sprintf("严格查询执行失败: params_group=%d, SQL=%s", groupIndex, query),
			)
		}

		batch, mapErr := OrmHandlerInstance.ormBatchStrict(rows, returnType)
		if mapErr != nil {
			return nil, NewQueryExceptionWithCause(
				mapErr,
				fmt.Sprintf("严格查询映射失败: params_group=%d, SQL=%s", groupIndex, query),
			)
		}
		results = append(results, batch...)
	}
	return results, nil
}

func findByIdStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	id any,
	entityType IDbEntity,
) (IDbEntity, error) {
	entity, err := loadByIdStrictContext(ctx, runner, base, id, entityType)
	if err != nil || entity == nil {
		return entity, err
	}
	entity.DeserializeAfterLoadDb()
	return entity, nil
}

func loadByIdStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	id any,
	entityType IDbEntity,
) (IDbEntity, error) {
	if validationErr := validateStrictRepositoryRead(ctx, runner, base, entityType); validationErr != nil {
		return nil, validationErr
	}
	if id == nil {
		return nil, NewValidationException("查询 ID 不能为 nil")
	}

	tableName, uidColumn, metadataErr := strictRepositoryMetadata(base, entityType)
	if metadataErr != nil {
		return nil, metadataErr
	}
	query := "SELECT * FROM " + tableName + " WHERE " + uidColumn + " = ?"
	if GetCrudPerformanceSettings().Snapshot().EnableSqlTemplateCache {
		query = GetSqlTemplateCache().GetFindByIdSQL(entityType, tableName, uidColumn)
	}

	results, queryErr := executeQueryStrictContextWithRunner(ctx, runner, query, [][]any{{id}}, entityType)
	if queryErr != nil {
		return nil, queryErr
	}
	entities, convertErr := strictEntitiesFromResults(results)
	if convertErr != nil {
		return nil, convertErr
	}
	if len(entities) == 0 {
		return nil, nil
	}
	return entities[0], nil
}

func findByIdsStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	ids []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	entities, err := loadByIdsStrictContext(ctx, runner, base, ids, entityType)
	if err != nil {
		return nil, err
	}
	deserializeStrictEntities(entities)
	return entities, nil
}

func loadByIdsStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	ids []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	if validationErr := validateStrictRepositoryRead(ctx, runner, base, entityType); validationErr != nil {
		return nil, validationErr
	}
	if len(ids) == 0 {
		return []IDbEntity{}, nil
	}

	validIDs := make([]any, 0, len(ids))
	for index, id := range ids {
		if id == nil {
			LogWarn("严格 FindByIds 跳过 nil ID: 索引=%d", index)
			continue
		}
		validIDs = append(validIDs, id)
	}
	if len(validIDs) == 0 {
		return []IDbEntity{}, nil
	}

	tableName, uidColumn, metadataErr := strictRepositoryMetadata(base, entityType)
	if metadataErr != nil {
		return nil, metadataErr
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().FindByIdsChunkSize
	if chunkSize <= 0 {
		chunkSize = len(validIDs)
	}
	rawResults := make([]any, 0, len(validIDs))
	for start := 0; start < len(validIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(validIDs) {
			end = len(validIDs)
		}
		chunk := validIDs[start:end]

		var query string
		if EnableAllocPoolEnabled() {
			query = appendFindByIdsSQL(tableName, uidColumn, len(chunk))
		} else {
			placeholders := make([]string, len(chunk))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			query = "SELECT * FROM " + tableName + " WHERE " + uidColumn + " IN (" + StringUtilsInstance.Join(placeholders, ",") + ")"
		}

		batch, queryErr := executeQueryStrictContextWithRunner(ctx, runner, query, [][]any{chunk}, entityType)
		if queryErr != nil {
			return nil, queryErr
		}
		rawResults = append(rawResults, batch...)
	}

	entities, convertErr := strictEntitiesFromResults(rawResults)
	if convertErr != nil {
		return nil, convertErr
	}
	return entities, nil
}

func findAllStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	entities, err := loadAllStrictContext(ctx, runner, base, entityType)
	if err != nil {
		return nil, err
	}
	deserializeStrictEntities(entities)
	return entities, nil
}

func loadAllStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	if validationErr := validateStrictRepositoryRead(ctx, runner, base, entityType); validationErr != nil {
		return nil, validationErr
	}
	tableName, _, metadataErr := strictRepositoryMetadata(base, entityType)
	if metadataErr != nil {
		return nil, metadataErr
	}

	results, queryErr := executeQueryStrictContextWithRunner(ctx, runner, "SELECT * FROM "+tableName, nil, entityType)
	if queryErr != nil {
		return nil, queryErr
	}
	entities, convertErr := strictEntitiesFromResults(results)
	if convertErr != nil {
		return nil, convertErr
	}
	return entities, nil
}

func findByConditionStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	condition string,
	params []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	entities, err := loadByConditionStrictContext(ctx, runner, base, condition, params, entityType)
	if err != nil {
		return nil, err
	}
	deserializeStrictEntities(entities)
	return entities, nil
}

func loadByConditionStrictContext(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	condition string,
	params []any,
	entityType IDbEntity,
) ([]IDbEntity, error) {
	if validationErr := validateStrictRepositoryRead(ctx, runner, base, entityType); validationErr != nil {
		return nil, validationErr
	}
	if condition == "" {
		return nil, NewValidationException("查询条件不能为空")
	}
	tableName, _, metadataErr := strictRepositoryMetadata(base, entityType)
	if metadataErr != nil {
		return nil, metadataErr
	}

	query := "SELECT * FROM " + tableName + " WHERE " + condition
	results, queryErr := executeQueryStrictContextWithRunner(ctx, runner, query, [][]any{params}, entityType)
	if queryErr != nil {
		return nil, queryErr
	}
	entities, convertErr := strictEntitiesFromResults(results)
	if convertErr != nil {
		return nil, convertErr
	}
	return entities, nil
}

func validateStrictRepositoryRead(
	ctx context.Context,
	runner strictRowsQueryer,
	base *BaseCrudRepository,
	entityType IDbEntity,
) error {
	if ctx == nil {
		return NewValidationException("context 不能为 nil")
	}
	if isNilStrictValue(runner) {
		return NewValidationException("严格查询 runner 不能为 nil")
	}
	if base == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	if isNilStrictValue(entityType) {
		return NewValidationException("实体类型不能为 nil")
	}
	if _, typeErr := strictOrmStructType(entityType); typeErr != nil {
		return typeErr
	}
	return nil
}

func strictRepositoryMetadata(base *BaseCrudRepository, entityType IDbEntity) (string, string, error) {
	tableName := base.getTableName(entityType)
	if tableName == "" {
		return "", "", NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}
	uidColumn := GetCrudManagerInstance().GetPrimaryKeyColumnName(entityType)
	if uidColumn == "" {
		uidColumn = "id"
	}
	return tableName, uidColumn, nil
}

func strictEntitiesFromResults(results []any) ([]IDbEntity, error) {
	entities := make([]IDbEntity, 0, len(results))
	for index, result := range results {
		entity, ok := result.(IDbEntity)
		if !ok || isNilStrictValue(entity) {
			return nil, NewQueryException(fmt.Sprintf("严格查询结果未实现 IDbEntity: index=%d, type=%T", index, result))
		}
		entities = append(entities, entity)
	}
	return entities, nil
}

func deserializeStrictEntities(entities []IDbEntity) {
	for _, entity := range entities {
		entity.DeserializeAfterLoadDb()
	}
}

func isNilStrictValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ StrictCrudRepository = (*BaseCrudRepository)(nil)
