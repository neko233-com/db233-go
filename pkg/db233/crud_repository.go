package db233

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// CrudRepository 是 CRUD 存储库接口，提供基本的 CRUD 操作。
// 所有实体必须实现 IDbEntity 接口。
type CrudRepository interface {
	// GetBindingDataSource 返回绑定的数据源。
	GetBindingDataSource() *sql.DB

	// GetDb 返回数据库实例。
	GetDb() *Db

	// Save 保存实体（必须实现 IDbEntity 接口）。
	Save(entity IDbEntity) error

	// SaveBatch 批量保存实体（必须实现 IDbEntity 接口）。
	SaveBatch(entities []IDbEntity) error

	// SaveBatchUpsert 批量 UPSERT（INSERT ... ON DUPLICATE KEY UPDATE）。
	SaveBatchUpsert(entities []IDbEntity) error

	// UpdateBatchUpsert 批量属性同步 UPSERT（语义同 SaveBatchUpsert，走 WAL 耐久写）。
	UpdateBatchUpsert(entities []IDbEntity) error

	// SaveBuffered 异步入队保存（高频写场景，需配合 FlushWriteBuffer）。
	SaveBuffered(entity IDbEntity) error

	// FlushWriteBuffer 同步刷盘写缓冲。
	FlushWriteBuffer() error

	// DeleteById 根据主键删除。
	DeleteById(id any, entityType IDbEntity) error

	// FindById 根据主键查找。
	FindById(id any, entityType IDbEntity) (IDbEntity, error)

	// FindByIds 根据主键列表批量查找（单表 IN 查询，支持分块）。
	FindByIds(ids []any, entityType IDbEntity) ([]IDbEntity, error)

	// FindByIdsMap 根据主键列表批量查找，返回 map[primaryKey]IDbEntity。
	FindByIdsMap(ids []any, entityType IDbEntity) (map[any]IDbEntity, error)

	// FindByIdConcurrent 并发按主键查询多个实体类型（登录加载多表场景）。
	FindByIdConcurrent(id any, entityTypes []IDbEntity, config *ConcurrentCrudConfig) []FindByIdConcurrentItem

	// FindAll 查找所有记录。
	FindAll(entityType IDbEntity) ([]IDbEntity, error)

	// FindByCondition 根据条件查找。
	FindByCondition(condition string, params []any, entityType IDbEntity) ([]IDbEntity, error)

	// Update 更新实体（必须实现 IDbEntity 接口）。
	Update(entity IDbEntity) error

	// UpdateBatch 批量更新（真批量：同表单 SQL UPSERT，跨表自动分组）。
	UpdateBatch(entities []IDbEntity) error

	// Count 统计数量。
	Count(entityType IDbEntity) (int64, error)
}

// BaseCrudRepository - 基础 CRUD 实现
type BaseCrudRepository struct {
	db           *Db
	writeBuffer  *WriteBuffer
	writeJournal *LocalWriteJournal
	journalMu    sync.RWMutex
	wbMu         sync.Mutex
	closed       bool
	closeMu      sync.Mutex
	closeOnce    sync.Once
	closeErr     error

	testHookMu     sync.Mutex
	testUpsertHook func([]IDbEntity) error // 仅测试注入
}

// ErrCrudRepositoryClosed 表示 Repository 的后台资源已关闭。
var ErrCrudRepositoryClosed = errors.New("CRUD Repository 已关闭")

// ErrWriteJournalCleanup 表示数据库写入已成功，但对应 WAL 条目清理失败。
// UPSERT 可安全重放；调用方必须保留该错误，不能把操作报告为完全成功。
var ErrWriteJournalCleanup = errors.New("database write succeeded but WAL cleanup failed")

// SetTestUpsertHook 测试专用：拦截 UpdateBatchUpsert（nil 恢复默认）。
func (r *BaseCrudRepository) SetTestUpsertHook(hook func([]IDbEntity) error) {
	if r == nil {
		return
	}
	r.testHookMu.Lock()
	defer r.testHookMu.Unlock()
	r.testUpsertHook = hook
}

// SetWriteJournal 绑定本地 WAL（InitGameDb 调用）。
func (r *BaseCrudRepository) SetWriteJournal(journal *LocalWriteJournal) {
	if r == nil {
		return
	}
	r.journalMu.Lock()
	defer r.journalMu.Unlock()
	r.writeJournal = journal
}

// GetWriteJournal 返回绑定的 WAL。
func (r *BaseCrudRepository) GetWriteJournal() *LocalWriteJournal {
	if r == nil {
		return nil
	}
	r.journalMu.RLock()
	defer r.journalMu.RUnlock()
	return r.writeJournal
}

// 创建基础 CRUD 存储库
func NewBaseCrudRepository(db *Db) *BaseCrudRepository {
	r := &BaseCrudRepository{db: db}
	if db != nil {
		r.writeJournal = db.WriteJournal
	}
	return r
}

// Close 停止并刷清当前 Repository 的写缓冲。可安全重复、并发调用。
func (r *BaseCrudRepository) Close() error {
	if r == nil {
		return nil
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return r.closeErr
	}
	databaseGeneration := ""
	releaseGeneration := func() {}
	if r.db != nil {
		var err error
		databaseGeneration, releaseGeneration, err = r.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return err
		}
	}
	defer releaseGeneration()
	return r.closeUnderGenerationLeaseLocked(databaseGeneration)
}

// closeUnderGenerationLease 要求调用方持有当前 Db generation 的读或写租约。
// 它让普通 Close 与 Db.Close/切代互斥，保证 unregister 前 pending 已刷完。
func (r *BaseCrudRepository) closeUnderGenerationLease(databaseGeneration string) error {
	if r == nil {
		return nil
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	return r.closeUnderGenerationLeaseLocked(databaseGeneration)
}

func (r *BaseCrudRepository) closeUnderGenerationLeaseLocked(databaseGeneration string) error {
	r.closeOnce.Do(func() {
		r.wbMu.Lock()
		r.closed = true
		buffer := r.writeBuffer
		r.wbMu.Unlock()
		if buffer != nil {
			if stopErr := buffer.stopUnderGenerationLease(databaseGeneration); stopErr != nil {
				r.closeErr = NewDb233ExceptionWithCause(stopErr, "停止写缓冲失败")
			}
		}
		if r.db != nil {
			r.db.unregisterBufferedRepository(r)
		}
	})
	return r.closeErr
}

// 获取绑定的数据源
func (r *BaseCrudRepository) GetBindingDataSource() *sql.DB {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.GetDataSource()
}

// 获取数据库实例
func (r *BaseCrudRepository) GetDb() *Db {
	if r == nil {
		return nil
	}
	return r.db
}

func (r *BaseCrudRepository) lockCurrentWriteGeneration() (string, func(), error) {
	if r == nil || r.db == nil {
		return "", func() {}, NewQueryException("数据库连接未初始化")
	}
	return r.db.lockCurrentDatabaseGeneration()
}

// 保存实体
func (r *BaseCrudRepository) Save(entity IDbEntity) error {
	// 参数验证
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
		return identifierErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()
	if r.GetWriteJournal() != nil {
		return r.saveBatchUpsertWithJournal([]IDbEntity{entity}, 1, databaseGeneration, false)
	}

	// 调用保存前的序列化钩子
	if err := runEntitySerializeHook(entity); err != nil {
		return err
	}

	// 获取表名
	tableName := r.getTableName(entity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 获取字段
	fields, fieldsErr := r.getFields(entity)
	if fieldsErr != nil {
		return fieldsErr
	}
	if len(fields) == 0 {
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段，请检查字段是否包含 db 标签", entity))
	}

	// 获取唯一ID列名（自动扫描 struct tag）
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entity)
	if uidColumn == "" {
		uidColumn = "id"
	}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	if identifierErr := validateRepositorySQLIdentifiers(tableName, uidColumn, fieldNames); identifierErr != nil {
		return identifierErr
	}

	// 获取主键值（自动从 struct 字段读取）
	uidValue := cm.GetPrimaryKeyValue(entity)

	// 构建 INSERT 语句
	columns := make([]string, 0, len(fields))
	placeholders := make([]string, 0, len(fields))
	values := make([]any, 0, len(fields))

	// 检查主键是否为自增主键
	isAutoIncrement := r.isAutoIncrementPrimaryKey(entity, uidColumn)

	for _, name := range fieldNames {
		value := fields[name]
		// 主键字段的特殊处理
		if name == uidColumn {
			// 检查值是否为零值
			if r.isZeroValue(value) {
				if isAutoIncrement {
					// 自增主键：零值时跳过，由数据库自动生成
					LogDebug("跳过自增主键字段: 表=%s, 主键列=%s (值为零值，将由数据库自动生成)", tableName, uidColumn)
					continue
				} else {
					// 非自增主键：零值时报错（业务主键必须提供有效值）
					LogError("非自增主键字段值为零值: 表=%s, 主键列=%s", tableName, uidColumn)
					return NewValidationException(fmt.Sprintf("主键字段 %s 不能为零值（0 或空字符串），请设置有效的主键值", uidColumn))
				}
			}
			// 主键有值，正常包含
			LogDebug("包含主键字段: 表=%s, 主键列=%s, 自增=%v", tableName, uidColumn, isAutoIncrement)
		}

		// 对于非主键字段，即使值为空也要包含（让数据库处理 NOT NULL 约束）
		// 如果值为 nil 或零值，提供默认值
		finalValue := r.getDefaultValueIfEmpty(value, name)
		if finalValue != value {
			LogDebug("为字段提供默认值: 表=%s, 字段=%s", tableName, name)
		}

		columns = append(columns, name)
		placeholders = append(placeholders, "?")
		values = append(values, finalValue)
	}

	if len(columns) == 0 {
		return NewValidationException(fmt.Sprintf("表 %s 没有可插入的字段（所有字段都为空或已跳过）", tableName))
	}

	// ========== UPSERT 逻辑：自动处理 INSERT 或 UPDATE ==========
	// 检查主键是否在 columns 中（用于判断是否需要 upsert）
	hasPrimaryKey := false
	for _, col := range columns {
		if col == uidColumn {
			hasPrimaryKey = true
			break
		}
	}

	// 强制使用 INSERT ... ON DUPLICATE KEY UPDATE（UPSERT 语法）
	// 优点：
	// 1. 避免主键冲突错误（Error 1062: Duplicate entry）
	// 2. 自动判断是 INSERT 还是 UPDATE
	// 3. 减少业务代码复杂度，无需手动判断记录是否存在
	var sql string
	var finalValues []any

	if hasPrimaryKey {
		// 有主键值，强制使用 INSERT ... ON DUPLICATE KEY UPDATE（UPSERT）
		// 相当于：如果主键不存在则插入，如果主键已存在则更新其他字段
		updateParts := make([]string, 0)
		for _, col := range columns {
			if col != uidColumn {
				// 只更新非主键字段（主键不能修改）
				updateParts = append(updateParts, col+" = VALUES("+col+")")
			}
		}

		if len(updateParts) > 0 {
			// 使用 ON DUPLICATE KEY UPDATE（强制 UPSERT）
			// MySQL 语法：INSERT INTO ... VALUES ... ON DUPLICATE KEY UPDATE ...
			sql = "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES (" + StringUtilsInstance.Join(placeholders, ",") + ") ON DUPLICATE KEY UPDATE " + StringUtilsInstance.Join(updateParts, ", ")
			finalValues = values
			LogDebug("执行 UPSERT (强制): 表=%s, 主键列=%s, 字段数=%d", tableName, uidColumn, len(columns))
		} else {
			// 只有主键字段，使用普通 INSERT IGNORE（避免重复错误）
			sql = "INSERT IGNORE INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES (" + StringUtilsInstance.Join(placeholders, ",") + ")"
			finalValues = values
			LogDebug("执行 INSERT IGNORE (仅主键): 表=%s, 主键列=%s", tableName, uidColumn)
		}
	} else {
		// 没有主键值（自增主键），使用普通 INSERT
		// 场景：id 为 0 或 nil，由数据库自动生成主键
		sql = "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES (" + StringUtilsInstance.Join(placeholders, ",") + ")"
		finalValues = values
		LogDebug("执行 INSERT (自增主键): 表=%s, 字段数=%d", tableName, len(columns))
	}

	result, err := r.db.execContext(context.Background(), sql, finalValues...)
	if err != nil {
		// 友好的错误提示
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%s", tableName, safeErrorForLog(err))
			recoveryErr := r.recordFailedOperationWithEntity("Save", tableName, sql, finalValues, uidValue, entity, databaseGeneration)
			r.triggerFaultTolerantReconnect()
			return NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		} else {
			LogError("保存实体失败: 表=%s, 错误=%s, %s", tableName, safeErrorForLog(err), sqlForRuntimeLog(sql))
			return NewQueryExceptionWithCause(err, fmt.Sprintf("保存实体到表 %s 失败", tableName))
		}
	}

	// 处理自增主键
	lastInsertId, err := result.LastInsertId()
	if err == nil && lastInsertId > 0 {
		r.setPrimaryKeyValue(entity, lastInsertId)
		LogDebug("自增主键已设置: 表=%s, 主键列=%s", tableName, uidColumn)
	}

	rowsAffected, rowsAffectedErr := result.RowsAffected()
	if rowsAffectedErr != nil {
		return NewQueryExceptionWithCause(rowsAffectedErr, fmt.Sprintf("获取表 %s 保存影响行数失败", tableName))
	}
	if rowsAffected == 1 {
		LogDebug("保存成功 (INSERT): 表=%s, 影响行数=%d", tableName, rowsAffected)
	} else if rowsAffected == 2 {
		LogDebug("保存成功 (UPDATE): 表=%s, 影响行数=%d (主键冲突，已更新)", tableName, rowsAffected)
	} else {
		LogDebug("保存完成: 表=%s, 影响行数=%d", tableName, rowsAffected)
	}

	return nil
}

// 设置主键值（支持嵌入结构体和多种主键标签方式）
func (r *BaseCrudRepository) setPrimaryKeyValue(entity any, id int64) {
	if entity == nil {
		return
	}
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return
	}

	cm := GetCrudManagerInstance()
	r.setPrimaryKeyValueRecursive(v, v.Type(), id, cm)
}

// 递归设置主键值
func (r *BaseCrudRepository) setPrimaryKeyValueRecursive(v reflect.Value, t reflect.Type, id int64, cm *CrudManager) bool {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i)

		// 处理嵌入结构体
		if field.Anonymous {
			embeddedValue := fieldValue
			embeddedType := field.Type

			if embeddedType.Kind() == reflect.Ptr {
				if embeddedValue.IsNil() {
					elementType := embeddedType.Elem()
					if !embeddedValue.CanSet() || elementType.Kind() != reflect.Struct ||
						!repositoryTypeContainsPrimaryKey(elementType, cm, make(map[reflect.Type]bool)) {
						continue
					}
					embeddedValue.Set(reflect.New(elementType))
				}
				embeddedValue = embeddedValue.Elem()
				embeddedType = embeddedType.Elem()
			}

			if embeddedType.Kind() == reflect.Struct {
				// 递归查找嵌入结构体中的主键字段
				if r.setPrimaryKeyValueRecursive(embeddedValue, embeddedType, id, cm) {
					return true
				}
			}
			continue
		}

		// 检查是否为主键或自增字段
		if cm.IsPrimaryKey(field) || cm.IsAutoIncrement(field) {
			if fieldValue.CanSet() {
				switch fieldValue.Kind() {
				case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8:
					fieldValue.SetInt(id)
					LogDebug("主键值已设置: 字段=%s", field.Name)
					return true
				case reflect.Uint, reflect.Uint64, reflect.Uint32, reflect.Uint16, reflect.Uint8:
					fieldValue.SetUint(uint64(id))
					LogDebug("主键值已设置: 字段=%s", field.Name)
					return true
				}
			}
		}
	}
	return false
}

func repositoryTypeContainsPrimaryKey(t reflect.Type, cm *CrudManager, visiting map[reflect.Type]bool) bool {
	if visiting[t] {
		return false
	}
	visiting[t] = true
	defer delete(visiting, t)
	for index := 0; index < t.NumField(); index++ {
		field := t.Field(index)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct && repositoryTypeContainsPrimaryKey(embeddedType, cm, visiting) {
				return true
			}
			continue
		}
		if cm.IsPrimaryKey(field) || cm.IsAutoIncrement(field) {
			return true
		}
	}
	return false
}

// getTableName 获取表名。
// entity: 实现了 IDbEntity 接口的实体。
// 返回: 表名。
func (r *BaseCrudRepository) getTableName(entity IDbEntity) string {
	if isNilStrictValue(entity) {
		return ""
	}
	// 直接调用 TableName() 方法
	tableName := entity.TableName()
	if tableName != "" {
		return tableName
	}

	// 如果 TableName() 返回空字符串，使用类型名转换为 snake_case（向后兼容）
	t := reflect.TypeOf(entity)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ""
	}
	return StringUtilsInstance.CamelToSnake(t.Name())
}

// 获取字段（支持嵌入结构体）
func (r *BaseCrudRepository) getFields(entity any) (map[string]any, error) {
	if EnableAllocPoolEnabled() {
		scratch := acquireFieldMap()
		if err := r.getFieldsInto(entity, scratch); err != nil {
			releaseFieldMap(scratch)
			return nil, err
		}
		out := make(map[string]any, len(scratch))
		for k, v := range scratch {
			out[k] = v
		}
		releaseFieldMap(scratch)
		return out, nil
	}
	fields := make(map[string]any)
	if err := r.getFieldsInto(entity, fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// getFieldsInto 扫描实体字段写入 fields（可复用 map，调用方负责 clear）。
func (r *BaseCrudRepository) getFieldsInto(entity any, fields map[string]any) error {
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	if fields == nil {
		return NewValidationException("字段目标 map 不能为 nil")
	}
	v := reflect.ValueOf(entity)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return NewValidationException("实体不能为 nil")
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return NewValidationException(fmt.Sprintf("实体必须是 struct 或 *struct，实际类型: %T", entity))
	}
	t := v.Type()
	entityTypeName := t.Name()
	return r.scanFieldsRecursive(v, t, entityTypeName, fields)
}

// 递归扫描字段（处理嵌入结构体）
func (r *BaseCrudRepository) scanFieldsRecursive(v reflect.Value, t reflect.Type, entityTypeName string, fields map[string]any) error {
	return r.scanFieldsRecursivePath(v, t, entityTypeName, fields, make(map[reflect.Type]bool))
}

func (r *BaseCrudRepository) scanFieldsRecursivePath(
	v reflect.Value,
	t reflect.Type,
	entityTypeName string,
	fields map[string]any,
	visiting map[reflect.Type]bool,
) error {
	if visiting[t] {
		return NewValidationException(fmt.Sprintf("实体包含递归匿名嵌入结构: %s", t))
	}
	visiting[t] = true
	defer delete(visiting, t)

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i)

		// 检查字段是否可导出（可访问）
		if !fieldValue.CanInterface() {
			LogDebug("跳过未导出字段: 实体=%s, 字段=%s (字段未导出，无法访问)", entityTypeName, field.Name)
			continue
		}

		// 处理嵌入结构体（Anonymous field）
		if field.Anonymous {
			embeddedType := field.Type
			embeddedValue := fieldValue

			// 如果是指针，需要解引用
			if embeddedType.Kind() == reflect.Ptr {
				if embeddedValue.IsNil() {
					embeddedType = embeddedType.Elem()
					if embeddedType.Kind() == reflect.Struct {
						// 仍扫描零值结构，确保同一实体类型的 SQL 列集合不依赖首条记录。
						if err := r.scanFieldsRecursivePath(reflect.Zero(embeddedType), embeddedType, entityTypeName, fields, visiting); err != nil {
							return err
						}
					}
					continue
				}
				embeddedValue = embeddedValue.Elem()
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归扫描
			if embeddedType.Kind() == reflect.Struct {
				LogDebug("递归扫描嵌入结构体: 实体=%s, 嵌入字段=%s", entityTypeName, field.Name)
				if err := r.scanFieldsRecursivePath(embeddedValue, embeddedType, entityTypeName, fields, visiting); err != nil {
					return err
				}
				continue
			}
		}

		// 解析 db 标签
		tag := field.Tag.Get("db")
		var columnName string
		var shouldSkip bool

		if tag == "-" {
			// 明确标记为跳过 (db:"-")
			LogDebug("跳过字段（db标签为'-'）: 实体=%s, 字段=%s", entityTypeName, field.Name)
			continue
		}

		if tag != "" {
			// 解析标签，获取列名（标签格式：column_name,options...）
			tagParts := strings.Split(tag, ",")
			columnName = strings.TrimSpace(tagParts[0])
			if columnName == "" || columnName == "-" {
				// 如果 db 标签的列名部分为空或为 "-"（如 db:"" 或 db:"-,xxx"），跳过该字段
				LogDebug("跳过字段（db标签列名为空或'-'）: 实体=%s, 字段=%s", entityTypeName, field.Name)
				continue
			}
			// 检查是否有 skip 选项
			for _, part := range tagParts[1:] {
				if strings.TrimSpace(part) == "skip" {
					shouldSkip = true
					break
				}
			}
		} else {
			// 如果没有 db 标签（tag == ""），跳过该字段
			// 要求必须显式声明 db 标签才会被处理
			LogDebug("跳过字段（无db标签）: 实体=%s, 字段=%s", entityTypeName, field.Name)
			continue
		}

		if shouldSkip {
			LogDebug("跳过字段（db标签包含'skip'选项）: 实体=%s, 字段=%s, 列名=%s", entityTypeName, field.Name, columnName)
			continue
		}

		// 获取字段值
		value := fieldValue.Interface()

		// 检查字段类型，处理复杂类型
		fieldType := fieldValue.Type()
		kind := fieldType.Kind()

		// 处理复杂类型（map、slice、array等）
		if r.isComplexType(kind, fieldType) {
			// 尝试序列化为 JSON
			jsonValue, err := r.serializeComplexType(value, fieldType)
			if err != nil {
				return NewValidationExceptionWithCause(
					err,
					fmt.Sprintf("复杂字段序列化失败: 实体=%s, 字段=%s, 列名=%s, 类型=%s",
						entityTypeName, field.Name, columnName, fieldType.String()),
				)
			}
			value = jsonValue
			LogDebug("序列化复杂类型字段: 实体=%s, 字段=%s, 列名=%s, 类型=%s",
				entityTypeName, field.Name, columnName, fieldType.String())
		}

		fields[columnName] = value
	}
	return nil
}

// 判断是否为复杂类型（需要序列化）
func (r *BaseCrudRepository) isComplexType(kind reflect.Kind, fieldType reflect.Type) bool {
	switch kind {
	case reflect.Map, reflect.Slice, reflect.Array:
		return true
	case reflect.Struct:
		// 检查是否为 time.Time（数据库原生支持）
		if fieldType == reflect.TypeOf(time.Time{}) {
			return false
		}
		// 其他结构体需要序列化
		return true
	case reflect.Ptr:
		// 指针类型需要进一步检查指向的类型
		elemType := fieldType.Elem()
		if elemType == reflect.TypeOf(time.Time{}) {
			return false
		}
		elemKind := elemType.Kind()
		if elemKind == reflect.Map || elemKind == reflect.Slice || elemKind == reflect.Array {
			return true
		}
		// 指针指向结构体，也需要序列化
		if elemKind == reflect.Struct {
			return true
		}
		return false
	default:
		return false
	}
}

// 序列化复杂类型为 JSON 字符串
func (r *BaseCrudRepository) serializeComplexType(value any, fieldType reflect.Type) (string, error) {
	// 如果值已经是字符串，直接返回
	if str, ok := value.(string); ok {
		return str, nil
	}

	// 如果值为 nil，返回空字符串
	if value == nil {
		return "", nil
	}

	// 使用 JSON 序列化
	if EnableAllocPoolEnabled() {
		return marshalJSONToString(value)
	}
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("JSON序列化失败: %w", err)
	}

	return string(jsonBytes), nil
}

// getDefaultValueIfEmpty 获取默认值（如果值为空）。
// 对于空值字段，根据类型提供合理的默认值，确保数据库 INSERT 不会因为缺少字段而失败。
func (r *BaseCrudRepository) getDefaultValueIfEmpty(value any, fieldName string) any {
	if value == nil {
		// nil 值，返回空字符串（数据库可以处理）
		LogDebug("字段值为 nil，使用空字符串作为默认值: 字段=%s", fieldName)
		return ""
	}

	v := reflect.ValueOf(value)

	// 处理指针类型
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			// nil 指针，返回空字符串
			LogDebug("字段值为 nil 指针，使用空字符串作为默认值: 字段=%s", fieldName)
			return ""
		}
		// 解引用指针，检查指向的值
		v = v.Elem()
		value = v.Interface()
	}

	// 检查是否为零值
	if !r.isZeroValue(value) {
		// 不是零值，直接返回原值
		return value
	}

	// 是零值，根据类型提供默认值
	switch v.Kind() {
	case reflect.String:
		// 字符串类型，空字符串已经是默认值，直接返回
		return ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// 整数类型，0 已经是默认值，直接返回
		return int64(0)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// 无符号整数类型，0 已经是默认值，直接返回
		return uint64(0)
	case reflect.Float32, reflect.Float64:
		// 浮点数类型，0.0 已经是默认值，直接返回
		return 0.0
	case reflect.Bool:
		// 布尔类型，false 已经是默认值，直接返回
		return false
	case reflect.Slice, reflect.Array:
		// 切片和数组，空值返回空 JSON 数组
		if v.IsNil() || v.Len() == 0 {
			LogDebug("字段值为空切片/数组，使用空 JSON 数组作为默认值: 字段=%s", fieldName)
			return "[]"
		}
		return value
	case reflect.Map:
		// Map 类型，空值返回空 JSON 对象
		if v.IsNil() || v.Len() == 0 {
			LogDebug("字段值为空 Map，使用空 JSON 对象作为默认值: 字段=%s", fieldName)
			return "{}"
		}
		return value
	default:
		// 其他类型，返回原值（让数据库处理）
		return value
	}
}

// 检查主键字段是否为自增主键
func (r *BaseCrudRepository) isAutoIncrementPrimaryKey(entity any, pkColumnName string) bool {
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	t := v.Type()

	return r.findAutoIncrementFieldRecursive(t, pkColumnName)
}

// 递归查找字段是否有 auto_increment 标签
func (r *BaseCrudRepository) findAutoIncrementFieldRecursive(t reflect.Type, targetColumnName string) bool {
	cm := GetCrudManagerInstance()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// 处理嵌入结构体
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				if r.findAutoIncrementFieldRecursive(embeddedType, targetColumnName) {
					return true
				}
			}
			continue
		}

		// 获取列名
		columnName := cm.GetColumnName(field)
		if columnName == targetColumnName {
			// 使用 CrudManager 的 IsAutoIncrement 方法
			return cm.IsAutoIncrement(field)
		}
	}
	return false
}

// 判断值是否为零值
func (r *BaseCrudRepository) isZeroValue(value any) bool {
	if value == nil {
		return true
	}

	v := reflect.ValueOf(value)

	// 处理指针类型
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return true
		}
		// 解引用指针，检查指向的值
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Interface:
		if v.IsNil() {
			return true
		}
		// 递归检查接口内部的值
		return r.isZeroValue(v.Interface())
	case reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil() || v.Len() == 0
	case reflect.Array:
		return v.Len() == 0
	case reflect.Struct:
		// 对于结构体，检查所有字段是否为零值
		for i := 0; i < v.NumField(); i++ {
			fieldValue := v.Field(i)
			// 跳过未导出的字段（无法调用 Interface()）
			if !fieldValue.CanInterface() {
				continue
			}
			if !r.isZeroValue(fieldValue.Interface()) {
				return false
			}
		}
		return true
	}
	return false
}

// 批量保存实体（真正的批量INSERT，一次SQL插入多条）
func (r *BaseCrudRepository) SaveBatch(entities []IDbEntity) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	// 参数验证
	if entities == nil {
		return NewValidationException("实体列表不能为 nil")
	}
	if len(entities) == 0 {
		return NewValidationException("实体列表不能为空")
	}

	// 过滤掉 nil 实体
	validEntities := make([]IDbEntity, 0, len(entities))
	for i, entity := range entities {
		if isNilStrictValue(entity) {
			LogWarn("批量保存跳过 nil 实体: 索引=%d", i)
			continue
		}
		if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
			return NewValidationExceptionWithCause(identifierErr, fmt.Sprintf("批量保存实体标识符非法: 索引=%d", i))
		}
		validEntities = append(validEntities, entity)
	}

	if len(validEntities) == 0 {
		return NewValidationException("没有有效的实体可保存")
	}
	if shapeErr := validateRepositoryBatchShapes(validEntities, r.getTableName); shapeErr != nil {
		return shapeErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchInsertChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultCrudPerformanceSettings().BatchInsertChunkSize
	}
	if chunkSize <= 0 {
		chunkSize = len(validEntities)
	}
	for _, group := range groupEntitiesByTable(validEntities, r.getTableName) {
		for start := 0; start < len(group); start += chunkSize {
			end := start + chunkSize
			if end > len(group) {
				end = len(group)
			}
			if err := r.saveBatchInsertOnce(group[start:end], databaseGeneration); err != nil {
				return err
			}
		}
	}
	return nil
}

// saveBatchInsertOnce 单次批量 INSERT（内部方法，不含分块逻辑）。
func (r *BaseCrudRepository) saveBatchInsertOnce(validEntities []IDbEntity, databaseGeneration string) error {
	if len(validEntities) == 0 {
		return nil
	}

	LogDebug("开始批量保存: 实体数量=%d", len(validEntities))

	// 获取第一个实体的表名和字段结构（假设所有实体类型相同）
	firstEntity := validEntities[0]
	if isNilStrictValue(firstEntity) {
		return NewValidationException("批量保存首个实体不能为 nil")
	}
	tableName := r.getTableName(firstEntity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 调用保存前的序列化钩子
	for index, entity := range validEntities {
		if isNilStrictValue(entity) {
			return NewValidationException(fmt.Sprintf("批量保存实体不能为 nil: 索引=%d", index))
		}
		if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
			return NewValidationExceptionWithCause(identifierErr, fmt.Sprintf("批量保存实体标识符非法: 索引=%d", index))
		}
		if r.getTableName(entity) != tableName {
			return NewValidationException("单个批量 INSERT 只能包含同一张表的实体")
		}
		if err := runEntitySerializeHook(entity); err != nil {
			return NewDb233ExceptionWithCause(err, fmt.Sprintf("批量保存序列化失败: 索引=%d", index))
		}
	}

	// 获取字段结构（使用第一个实体）
	firstFields, fieldsErr := r.getFields(firstEntity)
	if fieldsErr != nil {
		return fieldsErr
	}
	if len(firstFields) == 0 {
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段，请检查字段是否包含 db 标签", firstEntity))
	}

	// 获取主键信息
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(firstEntity)
	if uidColumn == "" {
		uidColumn = "id"
	}
	isAutoIncrement := r.isAutoIncrementPrimaryKey(firstEntity, uidColumn)

	// 确定要插入的列（排除自增主键的零值）
	columns := make([]string, 0, len(firstFields))
	for name, value := range firstFields {
		if name == uidColumn && isAutoIncrement && r.isZeroValue(value) {
			continue // 跳过自增主键的零值
		}
		columns = append(columns, name)
	}
	sort.Strings(columns)

	if len(columns) == 0 {
		return NewValidationException(fmt.Sprintf("表 %s 没有可插入的字段", tableName))
	}
	if identifierErr := validateRepositorySQLIdentifiers(tableName, uidColumn, columns); identifierErr != nil {
		return identifierErr
	}
	hasPrimaryKey := false
	for _, column := range columns {
		if column == uidColumn {
			hasPrimaryKey = true
			break
		}
	}
	if hasPrimaryKey {
		for _, entity := range validEntities {
			if r.isZeroValue(cm.GetPrimaryKeyValue(entity)) {
				return NewValidationException(fmt.Sprintf("批量 INSERT 要求所有实体主键 %s 非零值", uidColumn))
			}
		}
	} else if isAutoIncrement {
		for _, entity := range validEntities {
			if !r.isZeroValue(cm.GetPrimaryKeyValue(entity)) {
				return NewValidationException(fmt.Sprintf("批量自增 INSERT 要求所有实体主键 %s 均为零值", uidColumn))
			}
		}
	}

	// 构建批量INSERT SQL: INSERT INTO table (col1, col2) VALUES (?, ?), (?, ?), ...
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
		if fieldsErr := r.getFieldsInto(entity, fieldScratch); fieldsErr != nil {
			return fieldsErr
		}
		var rowValues []any
		if batchScratch != nil {
			batchScratch.rowValues = batchScratch.rowValues[:0]
			rowValues = batchScratch.rowValues
		} else {
			rowValues = make([]any, 0, len(columns))
		}
		for _, col := range columns {
			value, exists := fieldScratch[col]
			if !exists {
				value = r.getDefaultValueIfEmpty(nil, col)
			} else {
				value = r.getDefaultValueIfEmpty(value, col)
			}
			rowValues = append(rowValues, value)
		}
		placeholders = append(placeholders, rowPlaceholder)
		allValues = append(allValues, rowValues...)
	}

	var sql string
	if EnableAllocPoolEnabled() {
		sql = appendBatchInsertSQL(tableName, columns, placeholders)
	} else {
		sql = "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES " + StringUtilsInstance.Join(placeholders, ",")
	}

	LogDebug("执行批量INSERT: 表=%s, 记录数=%d, 字段数=%d, %s", tableName, len(validEntities), len(columns), sqlForRuntimeLog(sql))

	statement := &batchUpsertStatement{
		query:               sql,
		args:                allValues,
		tableName:           tableName,
		uidColumn:           uidColumn,
		columns:             columns,
		entities:            validEntities,
		assignAutoIncrement: isAutoIncrement && !hasPrimaryKey,
	}
	result, err := r.executeNonTransactionalBatchStatement(context.Background(), statement)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%s", tableName, safeErrorForLog(err))
			// 批量操作失败时，记录第一个实体的主键
			firstUidValue := cm.GetPrimaryKeyValue(firstEntity)
			recoveryErr := r.recordFailedOperation("SaveBatch", tableName, sql, allValues, firstUidValue, databaseGeneration)
			r.triggerFaultTolerantReconnect()
			return NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("批量保存失败: 表=%s, 错误=%s, %s", tableName, safeErrorForLog(err), sqlForRuntimeLog(sql))
		return NewQueryExceptionWithCause(err, fmt.Sprintf("批量保存到表 %s 失败", tableName))
	}

	// 处理自增主键（批量插入时，MySQL返回第一条记录的ID，后续ID是连续的）
	if assignIDs, idErr := r.batchAutoIncrementAction(statement, result); idErr == nil && assignIDs != nil {
		assignIDs()
	}

	rowsAffected, rowsAffectedErr := result.RowsAffected()
	if rowsAffectedErr != nil {
		return NewQueryExceptionWithCause(rowsAffectedErr, fmt.Sprintf("获取表 %s 批量保存影响行数失败", tableName))
	}
	LogDebug("批量保存完成: 表=%s, 影响行数=%d, 记录数=%d", tableName, rowsAffected, len(validEntities))

	return nil
}

// SaveBatchUpsert 批量 UPSERT（INSERT ... ON DUPLICATE KEY UPDATE）。
func (r *BaseCrudRepository) SaveBatchUpsert(entities []IDbEntity) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()
	return r.saveBatchUpsertUnderGenerationLease(entities, databaseGeneration)
}

func (r *BaseCrudRepository) saveBatchUpsertUnderGenerationLease(entities []IDbEntity, databaseGeneration string) error {
	return r.saveBatchUpsertUnderGenerationLeaseMode(entities, databaseGeneration, false)
}

func (r *BaseCrudRepository) saveBatchUpsertUnderGenerationLeaseMode(entities []IDbEntity, databaseGeneration string, entitiesPrepared bool) error {
	return r.saveBatchUpsertUnderGenerationLeaseModeWithFlushSource(entities, databaseGeneration, entitiesPrepared, "")
}

func (r *BaseCrudRepository) saveBatchUpsertUnderGenerationLeaseModeWithFlushSource(
	entities []IDbEntity,
	databaseGeneration string,
	entitiesPrepared bool,
	flushSource FlushWriteSource,
) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
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
			LogWarn("批量 UPSERT 跳过 nil 实体: 索引=%d", i)
			continue
		}
		if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
			return NewValidationExceptionWithCause(identifierErr, fmt.Sprintf("批量 UPSERT 实体标识符非法: 索引=%d", i))
		}
		validEntities = append(validEntities, entity)
	}
	if len(validEntities) == 0 {
		return NewValidationException("没有有效的实体可保存")
	}
	if shapeErr := validateRepositoryBatchShapes(validEntities, r.getTableName); shapeErr != nil {
		return shapeErr
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	groups := groupEntitiesByTable(validEntities, r.getTableName)
	for _, group := range groups {
		if err := r.saveBatchUpsertWithJournalAndFlushSource(group, chunkSize, databaseGeneration, entitiesPrepared, flushSource); err != nil {
			return err
		}
	}
	return nil
}

// groupEntitiesByTable 按表名分组（混合实体类型批量写时自动拆表，避免 JPA 式 N 次往返）。
func groupEntitiesByTable(entities []IDbEntity, tableNameOf func(IDbEntity) string) [][]IDbEntity {
	if len(entities) == 0 {
		return nil
	}
	order := make([]string, 0, 4)
	byTable := make(map[string][]IDbEntity)
	for _, entity := range entities {
		tableName := tableNameOf(entity)
		if _, ok := byTable[tableName]; !ok {
			order = append(order, tableName)
		}
		byTable[tableName] = append(byTable[tableName], entity)
	}
	groups := make([][]IDbEntity, 0, len(order))
	for _, tableName := range order {
		groups = append(groups, byTable[tableName])
	}
	return groups
}

// UpdateBatchUpsert 批量属性同步（游戏服高频写，WAL 保护不丢数据）。
func (r *BaseCrudRepository) UpdateBatchUpsert(entities []IDbEntity) error {
	return r.updateBatchUpsertWithFlushSource(entities, FlushWriteSourceManual)
}

func (r *BaseCrudRepository) updateBatchUpsertWithFlushSource(entities []IDbEntity, flushSource FlushWriteSource) error {
	if r == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	r.testHookMu.Lock()
	hook := r.testUpsertHook
	r.testHookMu.Unlock()
	if hook != nil {
		return hook(entities)
	}
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()
	return r.saveBatchUpsertUnderGenerationLeaseModeWithFlushSource(entities, databaseGeneration, false, flushSource)
}

func (r *BaseCrudRepository) updateBatchUpsertUnderGenerationLease(entities []IDbEntity, databaseGeneration string) error {
	if r == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	r.testHookMu.Lock()
	hook := r.testUpsertHook
	r.testHookMu.Unlock()
	if hook != nil {
		return hook(entities)
	}
	return r.saveBatchUpsertUnderGenerationLeaseModeWithFlushSource(
		entities,
		databaseGeneration,
		false,
		FlushWriteSourceSession,
	)
}

// updateBatchUpsertPreparedUnderGenerationLease 刷写已在首次 Flush 前执行过
// SerializeBeforeSaveDb 的实体。失败重试复用同一快照，不重复执行非幂等 hook。
func (r *BaseCrudRepository) updateBatchUpsertPreparedUnderGenerationLease(entities []IDbEntity, databaseGeneration string) error {
	return r.updateBatchUpsertPreparedUnderGenerationLeaseWithFlushSource(
		entities,
		databaseGeneration,
		FlushWriteSourceWriteBuffer,
	)
}

func (r *BaseCrudRepository) updateBatchUpsertPreparedUnderGenerationLeaseWithFlushSource(
	entities []IDbEntity,
	databaseGeneration string,
	flushSource FlushWriteSource,
) error {
	if r == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	r.testHookMu.Lock()
	hook := r.testUpsertHook
	r.testHookMu.Unlock()
	if hook != nil {
		return hook(entities)
	}
	return r.saveBatchUpsertUnderGenerationLeaseModeWithFlushSource(
		entities,
		databaseGeneration,
		true,
		flushSource,
	)
}

func (r *BaseCrudRepository) saveBatchUpsertWithJournal(validEntities []IDbEntity, chunkSize int, databaseGeneration string, entitiesPrepared bool) error {
	return r.saveBatchUpsertWithJournalAndFlushSource(validEntities, chunkSize, databaseGeneration, entitiesPrepared, "")
}

func (r *BaseCrudRepository) saveBatchUpsertWithJournalAndFlushSource(
	validEntities []IDbEntity,
	chunkSize int,
	databaseGeneration string,
	entitiesPrepared bool,
	flushSource FlushWriteSource,
) error {
	if chunkSize <= 0 {
		chunkSize = DefaultCrudPerformanceSettings().BatchUpsertChunkSize
	}
	if chunkSize <= 0 {
		chunkSize = len(validEntities)
	}
	journal := r.GetWriteJournal()
	if journal != nil {
		// 整个逻辑批次先一次性 durable merge，随后才允许任何 SQL；避免大批次
		// 每个 chunk 都全文件 rewrite+fsync。SQL 成功项最后一次性清理。
		var entries []*JournalEntry
		var appendErr error
		if entitiesPrepared {
			entries, appendErr = journal.appendPreparedEntitiesUnderGenerationLease("SaveBatchUpsert", validEntities, databaseGeneration)
		} else {
			entries, appendErr = journal.appendEntitiesUnderGenerationLease("SaveBatchUpsert", validEntities, databaseGeneration)
		}
		if appendErr != nil {
			return appendErr
		}
		if len(entries) != len(validEntities) {
			return NewDb233Exception("WAL durable 条目数量与批量 UPSERT 实体数量不一致")
		}

		succeededIDs := make([]string, 0, len(entries))
		writeErrors := make([]error, 0, 8)
		suppressedErrors := 0
		for start := 0; start < len(validEntities); start += chunkSize {
			end := start + chunkSize
			if end > len(validEntities) {
				end = len(validEntities)
			}
			chunk := validEntities[start:end]
			// WAL already owns durable recovery for this logical write. Recording the
			// same failure in FTM would create duplicate/stale replays and unbounded
			// queue growth during an outage.
			if err := r.saveBatchUpsertOnceInternalModeWithFlushSource(
				chunk,
				false,
				false,
				databaseGeneration,
				false,
				flushSource,
			); err != nil {
				writeErrors = appendBoundedRecoveryError(writeErrors, err, &suppressedErrors)
				continue
			}
			for _, entry := range entries[start:end] {
				succeededIDs = append(succeededIDs, entry.ID)
			}
		}
		if len(succeededIDs) > 0 {
			if err := journal.removeEntriesUnderGenerationLease(succeededIDs, databaseGeneration); err != nil {
				cleanupErr := NewDb233ExceptionWithCause(
					errors.Join(ErrWriteJournalCleanup, err),
					"数据库写入成功，但 WAL 清理失败；条目将保留并可安全重放",
				)
				LogError("WAL 删除已成功条目失败: %v", cleanupErr)
				writeErrors = appendBoundedRecoveryError(writeErrors, cleanupErr, &suppressedErrors)
			}
		}
		if suppressedErrors > 0 {
			writeErrors = append(writeErrors, fmt.Errorf("另有 %d 个批量 UPSERT 分块错误已省略", suppressedErrors))
		}
		if len(writeErrors) > 0 {
			LogWarn("批量 UPSERT WAL 分块未全部完成: 分块错误=%d, 已省略=%d", len(writeErrors), suppressedErrors)
		}
		return errors.Join(writeErrors...)
	}
	return r.saveBatchUpsertChunkedWithFlushSource(validEntities, chunkSize, databaseGeneration, !entitiesPrepared, flushSource)
}

func (r *BaseCrudRepository) saveBatchUpsertChunked(validEntities []IDbEntity, chunkSize int, databaseGeneration string, runSerializeHooks bool) error {
	return r.saveBatchUpsertChunkedWithFlushSource(validEntities, chunkSize, databaseGeneration, runSerializeHooks, "")
}

func (r *BaseCrudRepository) saveBatchUpsertChunkedWithFlushSource(
	validEntities []IDbEntity,
	chunkSize int,
	databaseGeneration string,
	runSerializeHooks bool,
	flushSource FlushWriteSource,
) error {
	if chunkSize <= 0 {
		chunkSize = DefaultCrudPerformanceSettings().BatchUpsertChunkSize
	}
	if chunkSize <= 0 {
		chunkSize = len(validEntities)
	}
	for start := 0; start < len(validEntities); start += chunkSize {
		end := start + chunkSize
		if end > len(validEntities) {
			end = len(validEntities)
		}
		if err := r.saveBatchUpsertOnceInternalModeWithFlushSource(
			validEntities[start:end],
			runSerializeHooks,
			true,
			databaseGeneration,
			true,
			flushSource,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *BaseCrudRepository) saveBatchUpsertOnce(validEntities []IDbEntity) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()
	return r.saveBatchUpsertOnceUnderGenerationLease(validEntities, databaseGeneration, true)
}

// saveBatchUpsertOncePrepared 用于 WAL 已完成 SerializeBeforeSaveDb 的路径。
func (r *BaseCrudRepository) saveBatchUpsertOncePreparedUnderGenerationLease(validEntities []IDbEntity, databaseGeneration string) error {
	// WAL 已独占 durable recovery；不得重复记录到 FTM。
	return r.saveBatchUpsertOnceInternalModeWithFlushSource(
		validEntities,
		false,
		false,
		databaseGeneration,
		false,
		FlushWriteSourceWALReplay,
	)
}

func (r *BaseCrudRepository) saveBatchUpsertOnceUnderGenerationLease(validEntities []IDbEntity, databaseGeneration string, recordRecovery bool) error {
	return r.saveBatchUpsertOnceInternal(validEntities, true, recordRecovery, databaseGeneration)
}

func (r *BaseCrudRepository) replayBatchUpsertOnceUnderGenerationLease(validEntities []IDbEntity, databaseGeneration string) error {
	return r.saveBatchUpsertOnceInternalModeWithFlushSource(
		validEntities,
		true,
		false,
		databaseGeneration,
		true,
		FlushWriteSourceFaultToleranceReplay,
	)
}

func (r *BaseCrudRepository) saveBatchUpsertOnceInternal(validEntities []IDbEntity, runSerializeHooks bool, recordRecovery bool, databaseGeneration string) error {
	return r.saveBatchUpsertOnceInternalMode(validEntities, runSerializeHooks, recordRecovery, databaseGeneration, true)
}

func (r *BaseCrudRepository) saveBatchUpsertOnceInternalMode(
	validEntities []IDbEntity,
	runSerializeHooks bool,
	recordRecovery bool,
	databaseGeneration string,
	logFailure bool,
) error {
	return r.saveBatchUpsertOnceInternalModeWithFlushSource(
		validEntities,
		runSerializeHooks,
		recordRecovery,
		databaseGeneration,
		logFailure,
		"",
	)
}

func (r *BaseCrudRepository) saveBatchUpsertOnceInternalModeWithFlushSource(
	validEntities []IDbEntity,
	runSerializeHooks bool,
	recordRecovery bool,
	databaseGeneration string,
	logFailure bool,
	flushSource FlushWriteSource,
) error {
	if len(validEntities) == 0 {
		return nil
	}
	if r.db == nil || r.db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}

	LogDebug("开始批量 UPSERT: 实体数量=%d", len(validEntities))

	statement, err := r.buildBatchUpsertStatementWithHooks(validEntities, runSerializeHooks)
	if err != nil {
		return err
	}
	statement.flushWriteSource = flushSource
	statement.flushEntityCount = len(validEntities)
	LogDebug("执行批量 UPSERT: 表=%s, 记录数=%d, 字段数=%d", statement.tableName, len(validEntities), len(statement.columns))

	result, err := r.executeNonTransactionalBatchStatement(context.Background(), statement)
	if err != nil {
		if isConnectionError(err) {
			if logFailure {
				LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%s", statement.tableName, safeErrorForLog(err))
			}
			var recoveryErr error
			if recordRecovery {
				firstUidValue := GetCrudManagerInstance().GetPrimaryKeyValue(validEntities[0])
				recoveryErr = r.recordFailedOperation("SaveBatchUpsert", statement.tableName, statement.query, statement.args, firstUidValue, databaseGeneration)
			}
			r.triggerFaultTolerantReconnect()
			return NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		}
		if logFailure {
			LogError("批量 UPSERT 失败: 表=%s, 错误=%s, %s", statement.tableName, safeErrorForLog(err), sqlForRuntimeLog(statement.query))
		}
		return NewQueryExceptionWithCause(err, fmt.Sprintf("批量 UPSERT 到表 %s 失败", statement.tableName))
	}
	// legacy 路径保持 LastInsertId 失败不影响已成功写入的既有语义。
	if assignIDs, idErr := r.batchAutoIncrementAction(statement, result); idErr == nil && assignIDs != nil {
		assignIDs()
	}

	rowsAffected, rowsAffectedErr := result.RowsAffected()
	if rowsAffectedErr != nil {
		return NewQueryExceptionWithCause(rowsAffectedErr, fmt.Sprintf("获取表 %s 批量 UPSERT 影响行数失败", statement.tableName))
	}
	LogDebug("批量 UPSERT 完成: 表=%s, 影响行数=%d, 记录数=%d", statement.tableName, rowsAffected, len(validEntities))
	return nil
}

// SaveBuffered 异步入队保存；缓冲满或未启用时同步 Save。
func (r *BaseCrudRepository) SaveBuffered(entity IDbEntity) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
		return identifierErr
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if !settings.WriteBufferEnabled {
		return r.Save(entity)
	}
	wb, err := r.ensureWriteBuffer(settings)
	if err != nil {
		return err
	}
	queued, err := wb.Enqueue(entity)
	if err != nil {
		return err
	}
	if !queued {
		return r.Save(entity)
	}
	return nil
}

// FlushWriteBuffer 同步刷盘写缓冲。
func (r *BaseCrudRepository) FlushWriteBuffer() error {
	if r == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	r.wbMu.Lock()
	wb := r.writeBuffer
	r.wbMu.Unlock()
	if wb == nil {
		return nil
	}
	return wb.Flush()
}

// flushWriteBufferUnderGenerationLease 仅供 Db generation transition 使用。
// 调用方必须持有 Db generation 写锁。
func (r *BaseCrudRepository) flushWriteBufferUnderGenerationLease(expectedGeneration string) error {
	if r == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	r.wbMu.Lock()
	wb := r.writeBuffer
	r.wbMu.Unlock()
	if wb == nil {
		return nil
	}
	return wb.flushUnderGenerationLease(expectedGeneration)
}

func (r *BaseCrudRepository) rotateWriteBufferDatabaseGeneration(generation string) {
	if r == nil {
		return
	}
	r.wbMu.Lock()
	wb := r.writeBuffer
	r.wbMu.Unlock()
	if wb != nil {
		wb.rotateDatabaseGeneration(generation)
	}
}

func (r *BaseCrudRepository) ensureWriteBuffer(settings CrudPerformanceSettings) (*WriteBuffer, error) {
	if r == nil {
		return nil, NewValidationException("Repository 不能为 nil")
	}
	r.wbMu.Lock()
	closed := r.closed
	r.wbMu.Unlock()
	if closed {
		return nil, ErrCrudRepositoryClosed
	}
	generation := ""
	releaseGeneration := func() {}
	if r != nil && r.db != nil {
		current, release, err := r.db.lockCurrentDatabaseGeneration()
		if err != nil {
			return nil, err
		}
		generation = current
		releaseGeneration = release
	}
	defer releaseGeneration()
	return r.ensureWriteBufferUnderGenerationLease(settings, generation)
}

// ensureWriteBufferUnderGenerationLease 由已持有 Db generation 租约的调用方
// 使用，避免 RWMutex 在有等待写者时重入 RLock 自锁。
func (r *BaseCrudRepository) ensureWriteBufferUnderGenerationLease(
	settings CrudPerformanceSettings,
	generation string,
) (*WriteBuffer, error) {
	if r == nil {
		return nil, NewValidationException("Repository 不能为 nil")
	}
	r.wbMu.Lock()
	defer r.wbMu.Unlock()
	if r.closed {
		return nil, ErrCrudRepositoryClosed
	}
	if r.writeBuffer == nil {
		if r.db != nil && !r.db.registerBufferedRepository(r) {
			if r.db.isDatabaseGenerationUnavailable() {
				return nil, ErrDatabaseGenerationBlocked
			}
			r.closed = true
			return nil, ErrCrudRepositoryClosed
		}
		r.writeBuffer = newWriteBufferForGeneration(r, generation)
		r.writeBuffer.Start(settings)
	}
	return r.writeBuffer, nil
}

// tryEnqueueOwnedSnapshotUnderGenerationLease 只尝试转移到 WriteBuffer。
// queued=true 才表示 Repository 接管所有权；false/error 时快照
// 仍归调用方，且未执行序列化 hook。
func (r *BaseCrudRepository) tryEnqueueOwnedSnapshotUnderGenerationLease(
	entity IDbEntity,
	databaseGeneration string,
) (bool, error) {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return false, readyErr
	}
	if isNilStrictValue(entity) {
		return false, NewValidationException("实体快照不能为 nil")
	}
	if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
		return false, identifierErr
	}
	settings := GetCrudPerformanceSettings().Snapshot()
	if !settings.WriteBufferEnabled {
		return false, nil
	}
	wb, err := r.ensureWriteBufferUnderGenerationLease(settings, databaseGeneration)
	if err != nil {
		return false, err
	}
	return wb.enqueueOwnedSnapshotUnderGenerationLease(entity, databaseGeneration)
}

func (r *BaseCrudRepository) saveSnapshotSynchronouslyUnderGenerationLease(
	entity IDbEntity,
	databaseGeneration string,
) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	if isNilStrictValue(entity) {
		return NewValidationException("实体快照不能为 nil")
	}
	if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
		return identifierErr
	}
	return r.updateBatchUpsertUnderGenerationLease([]IDbEntity{entity}, databaseGeneration)
}

func (r *BaseCrudRepository) DeleteById(id any, entityType IDbEntity) error {
	// 参数验证
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	if isNilStrictValue(entityType) {
		return NewValidationException("实体类型不能为 nil")
	}
	if id == nil {
		return NewValidationException("删除ID不能为 nil")
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 使用自动扫描获取唯一ID列名
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entityType)
	if uidColumn == "" {
		uidColumn = "id"
	}
	if identifierErr := validateRepositorySQLIdentifiers(tableName, uidColumn, nil); identifierErr != nil {
		return identifierErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()

	sql := "DELETE FROM " + tableName + " WHERE " + uidColumn + " = ?"
	LogDebug("执行 DELETE: 表=%s, 主键列=%s, %s", tableName, uidColumn, sqlForRuntimeLog(sql))

	result, err := r.db.execContext(context.Background(), sql, id)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%s", tableName, safeErrorForLog(err))
			recoveryErr := r.recordFailedOperation("Delete", tableName, sql, []any{id}, id, databaseGeneration)
			r.triggerFaultTolerantReconnect()
			return NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("删除实体失败: 表=%s, 错误=%s, %s", tableName, safeErrorForLog(err), sqlForRuntimeLog(sql))
		return NewQueryExceptionWithCause(err, fmt.Sprintf("删除表 %s 中的记录失败", tableName))
	}

	affectedRows, affectedErr := result.RowsAffected()
	if affectedErr != nil {
		return NewQueryExceptionWithCause(affectedErr, fmt.Sprintf("获取表 %s 删除影响行数失败", tableName))
	}
	if affectedRows == 0 {
		LogWarn("删除无影响: 表=%s, 可能记录不存在", tableName)
	} else {
		LogDebug("删除成功: 表=%s, 影响行数=%d", tableName, affectedRows)
	}

	return nil
}

func (r *BaseCrudRepository) FindById(id any, entityType IDbEntity) (IDbEntity, error) {
	return r.findByIdCompatibleContext(context.Background(), id, entityType)
}

// FindByIds 根据主键列表批量查找（单表 IN 查询）。
func (r *BaseCrudRepository) FindByIds(ids []any, entityType IDbEntity) ([]IDbEntity, error) {
	return r.findByIdsCompatibleContext(context.Background(), ids, entityType)
}

// FindByIdsMap 根据主键列表批量查找，返回 map[primaryKey]IDbEntity。
func (r *BaseCrudRepository) FindByIdsMap(ids []any, entityType IDbEntity) (map[any]IDbEntity, error) {
	list, err := r.FindByIds(ids, entityType)
	if err != nil {
		return nil, err
	}
	cm := GetCrudManagerInstance()
	result := make(map[any]IDbEntity, len(list))
	for _, entity := range list {
		if isNilStrictValue(entity) {
			continue
		}
		pk := cm.GetPrimaryKeyValue(entity)
		result[pk] = entity
	}
	return result, nil
}

func (r *BaseCrudRepository) FindAll(entityType IDbEntity) ([]IDbEntity, error) {
	return r.findAllCompatibleContext(context.Background(), entityType)
}

func (r *BaseCrudRepository) FindByCondition(condition string, params []any, entityType IDbEntity) ([]IDbEntity, error) {
	return r.findByConditionCompatibleContext(context.Background(), condition, params, entityType)
}

func (r *BaseCrudRepository) Update(entity IDbEntity) error {
	// 参数验证
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	if identifierErr := validateRepositoryEntityIdentifiers(entity); identifierErr != nil {
		return identifierErr
	}
	databaseGeneration, releaseGeneration, generationErr := r.lockCurrentWriteGeneration()
	if generationErr != nil {
		return generationErr
	}
	defer releaseGeneration()

	// 调用保存前的序列化钩子
	if err := runEntitySerializeHook(entity); err != nil {
		return err
	}

	// 获取表名
	tableName := r.getTableName(entity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 获取字段
	fields, fieldsErr := r.getFields(entity)
	if fieldsErr != nil {
		return fieldsErr
	}
	if len(fields) == 0 {
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段", entity))
	}

	// 使用自动扫描获取唯一ID列名
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entity)
	if uidColumn == "" {
		uidColumn = "id"
	}
	fieldNames := make([]string, 0, len(fields))
	for name := range fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	if identifierErr := validateRepositorySQLIdentifiers(tableName, uidColumn, fieldNames); identifierErr != nil {
		return identifierErr
	}

	// 获取唯一ID值
	id, exists := fields[uidColumn]
	if !exists {
		return NewValidationException(fmt.Sprintf("实体缺少唯一ID字段 %s，无法执行更新操作", uidColumn))
	}

	// 检查ID是否为空
	if r.isZeroValue(id) {
		return NewValidationException(fmt.Sprintf("实体的唯一ID字段 %s 为空，无法执行更新操作", uidColumn))
	}

	setParts := make([]string, 0)
	values := make([]any, 0)

	for _, name := range fieldNames {
		value := fields[name]
		if name != uidColumn {
			setParts = append(setParts, name+" = ?")
			values = append(values, value)
		}
	}

	if len(setParts) == 0 {
		return NewValidationException(fmt.Sprintf("没有可更新的字段（除了主键 %s）", uidColumn))
	}

	values = append(values, id)

	sql := "UPDATE " + tableName + " SET " + StringUtilsInstance.Join(setParts, ", ") + " WHERE " + uidColumn + " = ?"
	LogDebug("执行 UPDATE: 表=%s, 主键列=%s, 更新字段数=%d, %s", tableName, uidColumn, len(setParts), sqlForRuntimeLog(sql))

	result, err := r.db.execContext(context.Background(), sql, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%s", tableName, safeErrorForLog(err))
			recoveryErr := r.recordFailedOperation("Update", tableName, sql, values, id, databaseGeneration)
			r.triggerFaultTolerantReconnect()
			return NewQueryExceptionWithCause(errors.Join(err, recoveryErr), "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("更新实体失败: 表=%s, 错误=%s, %s", tableName, safeErrorForLog(err), sqlForRuntimeLog(sql))
		return NewQueryExceptionWithCause(err, fmt.Sprintf("更新表 %s 中的记录失败", tableName))
	}

	rowsAffected, rowsAffectedErr := result.RowsAffected()
	if rowsAffectedErr != nil {
		return NewQueryExceptionWithCause(rowsAffectedErr, fmt.Sprintf("获取表 %s 更新影响行数失败", tableName))
	}
	if rowsAffected == 0 {
		LogWarn("更新无影响: 表=%s, 可能记录不存在", tableName)
	} else {
		LogDebug("更新成功: 表=%s, 影响行数=%d", tableName, rowsAffected)
	}

	return nil
}

func (r *BaseCrudRepository) UpdateBatch(entities []IDbEntity) error {
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return readyErr
	}
	if entities == nil {
		return NewValidationException("实体列表不能为 nil")
	}
	if len(entities) == 0 {
		return NewValidationException("实体列表不能为空")
	}
	LogDebug("UpdateBatch 真批量 UPSERT: 实体数量=%d", len(entities))
	return r.SaveBatchUpsert(entities)
}

func (r *BaseCrudRepository) Count(entityType IDbEntity) (int64, error) {
	// 参数验证
	if readyErr := r.validateSQLReady(); readyErr != nil {
		return 0, readyErr
	}
	if isNilStrictValue(entityType) {
		return 0, NewValidationException("实体类型不能为 nil")
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return 0, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}
	if identifierErr := validateRepositoryTableIdentifier(tableName); identifierErr != nil {
		return 0, identifierErr
	}

	sql := "SELECT COUNT(*) FROM " + tableName
	LogDebug("执行计数查询: 表=%s, %s", tableName, sqlForRuntimeLog(sql))

	var count int64
	err := r.db.DataSource.QueryRow(sql).Scan(&count)
	if err != nil {
		LogError("计数查询失败: 表=%s, 错误=%s, %s", tableName, safeErrorForLog(err), sqlForRuntimeLog(sql))
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("统计表 %s 的记录数失败", tableName))
	}

	LogDebug("计数成功: 表=%s, 总数=%d", tableName, count)
	return count, nil
}

// 记录失败操作（连接异常时）
func (r *BaseCrudRepository) recordFailedOperation(operation string, tableName string, sql string, params []any, primaryKey any, databaseGeneration string) error {
	return r.recordFailedOperationWithEntity(operation, tableName, sql, params, primaryKey, nil, databaseGeneration)
}

func (r *BaseCrudRepository) recordFailedOperationWithEntity(operation string, tableName string, sql string, params []any, primaryKey any, entity IDbEntity, databaseGeneration string) error {
	if r == nil || r.db == nil {
		return nil
	}
	manager := r.db.faultTolerantManagerSnapshot()
	if manager == nil {
		return nil
	}
	op := &FailedOperation{
		Operation:  operation,
		SQL:        sql,
		Params:     toAnySlice(params),
		TableName:  tableName,
		PrimaryKey: primaryKey,
	}
	if entity != nil {
		op.EntityTypeName = EntityTypeName(entity)
		// SQL 参数构造前已经执行过 SerializeBeforeSaveDb；这里只做快照，
		// 否则失败记录会让同一逻辑写入重复执行用户 hook。
		if data, err := json.Marshal(entity); err == nil {
			op.EntityJSON = data
		} else {
			LogWarn("失败操作实体快照序列化失败，将回退到 SQL 参数重放: operation=%s, table=%s, err=%s", safeValueForLog(operation), safeValueForLog(tableName), safeErrorForLog(err))
		}
	}
	if err := manager.recordFailedOperationUnderGenerationLease(op, databaseGeneration); err != nil {
		LogError("失败操作未能持久化: operation=%s, table=%s, err=%s", safeValueForLog(operation), safeValueForLog(tableName), safeErrorForLog(err))
		return fmt.Errorf("持久化失败操作到恢复队列失败: %w", err)
	}
	return nil
}

func (r *BaseCrudRepository) triggerFaultTolerantReconnect() {
	if r == nil || r.db == nil {
		return
	}
	manager := r.db.faultTolerantManagerSnapshot()
	if manager == nil || manager.dbConfig == nil {
		return
	}
	manager.CheckAndReconnect()
}

func (r *BaseCrudRepository) validateSQLReady() error {
	if r == nil {
		return NewValidationException("Repository 不能为 nil")
	}
	if r.db == nil || r.db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}
	return nil
}
