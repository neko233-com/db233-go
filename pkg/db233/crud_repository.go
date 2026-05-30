package db233

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
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
	wbMu         sync.Mutex

	testHookMu     sync.Mutex
	testUpsertHook func([]IDbEntity) error // 仅测试注入
}

// SetTestUpsertHook 测试专用：拦截 UpdateBatchUpsert（nil 恢复默认）。
func (r *BaseCrudRepository) SetTestUpsertHook(hook func([]IDbEntity) error) {
	r.testHookMu.Lock()
	defer r.testHookMu.Unlock()
	r.testUpsertHook = hook
}

// SetWriteJournal 绑定本地 WAL（InitGameDb 调用）。
func (r *BaseCrudRepository) SetWriteJournal(journal *LocalWriteJournal) {
	r.writeJournal = journal
}

// GetWriteJournal 返回绑定的 WAL。
func (r *BaseCrudRepository) GetWriteJournal() *LocalWriteJournal {
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

// 获取绑定的数据源
func (r *BaseCrudRepository) GetBindingDataSource() *sql.DB {
	return r.db.GetDataSource()
}

// 获取数据库实例
func (r *BaseCrudRepository) GetDb() *Db {
	return r.db
}

// 保存实体
func (r *BaseCrudRepository) Save(entity IDbEntity) error {
	// 参数验证
	if entity == nil {
		return NewValidationException("实体不能为 nil")
	}
	if r.writeJournal != nil {
		return r.saveBatchUpsertWithJournal([]IDbEntity{entity}, 1)
	}

	// 调用保存前的序列化钩子
	entity.SerializeBeforeSaveDb()

	// 获取表名
	tableName := r.getTableName(entity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 获取字段
	fields := r.getFields(entity)
	if len(fields) == 0 {
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段，请检查字段是否包含 db 标签", entity))
	}

	// 获取唯一ID列名（自动扫描 struct tag）
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entity)
	if uidColumn == "" {
		uidColumn = "id"
	}

	// 获取主键值（自动从 struct 字段读取）
	uidValue := cm.GetPrimaryKeyValue(entity)

	// 构建 INSERT 语句
	columns := make([]string, 0, len(fields))
	placeholders := make([]string, 0, len(fields))
	values := make([]any, 0, len(fields))

	// 检查主键是否为自增主键
	isAutoIncrement := r.isAutoIncrementPrimaryKey(entity, uidColumn)

	for name, value := range fields {
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
			LogDebug("包含主键字段: 表=%s, 主键列=%s, 主键值=%v, 自增=%v", tableName, uidColumn, value, isAutoIncrement)
		}

		// 对于非主键字段，即使值为空也要包含（让数据库处理 NOT NULL 约束）
		// 如果值为 nil 或零值，提供默认值
		finalValue := r.getDefaultValueIfEmpty(value, name)
		if finalValue != value {
			LogDebug("为字段提供默认值: 表=%s, 字段=%s, 原值=%v, 默认值=%v", tableName, name, value, finalValue)
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
			LogDebug("执行 UPSERT (强制): 表=%s, 主键列=%s, 主键值=%v, 字段数=%d", tableName, uidColumn, uidValue, len(columns))
		} else {
			// 只有主键字段，使用普通 INSERT IGNORE（避免重复错误）
			sql = "INSERT IGNORE INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES (" + StringUtilsInstance.Join(placeholders, ",") + ")"
			finalValues = values
			LogDebug("执行 INSERT IGNORE (仅主键): 表=%s, 主键列=%s, 主键值=%v", tableName, uidColumn, uidValue)
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
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%v", tableName, err)
			r.recordFailedOperationWithEntity("Save", tableName, sql, finalValues, uidValue, entity)
			if r.db.FaultTolerantMgr != nil {
				r.db.FaultTolerantMgr.CheckAndReconnect()
			}
			return NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		} else {
			LogError("保存实体失败: 表=%s, 错误=%v, SQL=%s", tableName, err, sql)
			return NewQueryExceptionWithCause(err, fmt.Sprintf("保存实体到表 %s 失败", tableName))
		}
	}

	// 处理自增主键
	lastInsertId, err := result.LastInsertId()
	if err == nil && lastInsertId > 0 {
		r.setPrimaryKeyValue(entity, lastInsertId)
		LogDebug("自增主键已设置: 表=%s, 主键列=%s, 值=%d", tableName, uidColumn, lastInsertId)
	}

	rowsAffected, _ := result.RowsAffected()
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
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
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
					continue
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
					LogDebug("主键值已设置: 字段=%s, 值=%d", field.Name, id)
					return true
				case reflect.Uint, reflect.Uint64, reflect.Uint32, reflect.Uint16, reflect.Uint8:
					fieldValue.SetUint(uint64(id))
					LogDebug("主键值已设置: 字段=%s, 值=%d", field.Name, id)
					return true
				}
			}
		}
	}
	return false
}

// getTableName 获取表名。
// entity: 实现了 IDbEntity 接口的实体。
// 返回: 表名。
func (r *BaseCrudRepository) getTableName(entity IDbEntity) string {
	// 直接调用 TableName() 方法
	tableName := entity.TableName()
	if tableName != "" {
		return tableName
	}

	// 如果 TableName() 返回空字符串，使用类型名转换为 snake_case（向后兼容）
	t := reflect.TypeOf(entity)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return StringUtilsInstance.CamelToSnake(t.Name())
}

// 获取字段（支持嵌入结构体）
func (r *BaseCrudRepository) getFields(entity any) map[string]any {
	if EnableAllocPoolEnabled() {
		scratch := acquireFieldMap()
		r.getFieldsInto(entity, scratch)
		out := make(map[string]any, len(scratch))
		for k, v := range scratch {
			out[k] = v
		}
		releaseFieldMap(scratch)
		return out
	}
	fields := make(map[string]any)
	r.getFieldsInto(entity, fields)
	return fields
}

// getFieldsInto 扫描实体字段写入 fields（可复用 map，调用方负责 clear）。
func (r *BaseCrudRepository) getFieldsInto(entity any, fields map[string]any) {
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	t := v.Type()
	entityTypeName := t.Name()
	r.scanFieldsRecursive(v, t, entityTypeName, fields)
}

// 递归扫描字段（处理嵌入结构体）
func (r *BaseCrudRepository) scanFieldsRecursive(v reflect.Value, t reflect.Type, entityTypeName string, fields map[string]any) {
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
					LogDebug("跳过 nil 嵌入结构体: 实体=%s, 字段=%s", entityTypeName, field.Name)
					continue
				}
				embeddedValue = embeddedValue.Elem()
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归扫描
			if embeddedType.Kind() == reflect.Struct {
				LogDebug("递归扫描嵌入结构体: 实体=%s, 嵌入字段=%s", entityTypeName, field.Name)
				r.scanFieldsRecursive(embeddedValue, embeddedType, entityTypeName, fields)
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
				LogWarn("跳过复杂类型字段（序列化失败）: 实体=%s, 字段=%s, 列名=%s, 类型=%s, 错误=%v",
					entityTypeName, field.Name, columnName, fieldType.String(), err)
				continue
			}
			value = jsonValue
			LogDebug("序列化复杂类型字段: 实体=%s, 字段=%s, 列名=%s, 类型=%s",
				entityTypeName, field.Name, columnName, fieldType.String())
		}

		fields[columnName] = value
	}
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
		if entity == nil {
			LogWarn("批量保存跳过 nil 实体: 索引=%d", i)
			continue
		}
		validEntities = append(validEntities, entity)
	}

	if len(validEntities) == 0 {
		return NewValidationException("没有有效的实体可保存")
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchInsertChunkSize
	for start := 0; start < len(validEntities); start += chunkSize {
		end := start + chunkSize
		if end > len(validEntities) {
			end = len(validEntities)
		}
		if err := r.saveBatchInsertOnce(validEntities[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// saveBatchInsertOnce 单次批量 INSERT（内部方法，不含分块逻辑）。
func (r *BaseCrudRepository) saveBatchInsertOnce(validEntities []IDbEntity) error {
	if len(validEntities) == 0 {
		return nil
	}

	LogDebug("开始批量保存: 实体数量=%d", len(validEntities))

	// 获取第一个实体的表名和字段结构（假设所有实体类型相同）
	firstEntity := validEntities[0]
	tableName := r.getTableName(firstEntity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 调用保存前的序列化钩子
	for _, entity := range validEntities {
		entity.SerializeBeforeSaveDb()
	}

	// 获取字段结构（使用第一个实体）
	firstFields := r.getFields(firstEntity)
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

	if len(columns) == 0 {
		return NewValidationException(fmt.Sprintf("表 %s 没有可插入的字段", tableName))
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
		r.getFieldsInto(entity, fieldScratch)
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

	LogDebug("执行批量INSERT: 表=%s, 记录数=%d, 字段数=%d, SQL=%s", tableName, len(validEntities), len(columns), sql)

	result, err := r.db.execContext(context.Background(), sql, allValues...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%v", tableName, err)
			// 批量操作失败时，记录第一个实体的主键
			firstUidValue := cm.GetPrimaryKeyValue(firstEntity)
			r.recordFailedOperation("SaveBatch", tableName, sql, allValues, firstUidValue)
			if r.db.FaultTolerantMgr != nil {
				r.db.FaultTolerantMgr.CheckAndReconnect()
			}
			return NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("批量保存失败: 表=%s, 错误=%v, SQL=%s", tableName, err, sql)
		return NewQueryExceptionWithCause(err, fmt.Sprintf("批量保存到表 %s 失败", tableName))
	}

	// 处理自增主键（批量插入时，MySQL返回第一条记录的ID，后续ID是连续的）
	if isAutoIncrement {
		lastInsertId, err := result.LastInsertId()
		if err == nil && lastInsertId > 0 {
			// MySQL批量INSERT时，LastInsertId()返回第一条记录的ID
			// 后续记录的ID是连续的（ID, ID+1, ID+2, ...）
			// 为所有实体设置自增主键
			for i, entity := range validEntities {
				entityId := lastInsertId + int64(i)
				r.setPrimaryKeyValue(entity, entityId)
				LogDebug("批量INSERT自增主键已设置: 表=%s, 记录索引=%d, ID=%d", tableName, i, entityId)
			}
			LogDebug("批量INSERT自增主键已设置: 表=%s, 第一条记录ID=%d, 最后一条记录ID=%d, 总记录数=%d",
				tableName, lastInsertId, lastInsertId+int64(len(validEntities)-1), len(validEntities))
		}
	}

	rowsAffected, _ := result.RowsAffected()
	LogDebug("批量保存完成: 表=%s, 影响行数=%d, 记录数=%d", tableName, rowsAffected, len(validEntities))

	return nil
}

// SaveBatchUpsert 批量 UPSERT（INSERT ... ON DUPLICATE KEY UPDATE）。
func (r *BaseCrudRepository) SaveBatchUpsert(entities []IDbEntity) error {
	if entities == nil {
		return NewValidationException("实体列表不能为 nil")
	}
	if len(entities) == 0 {
		return NewValidationException("实体列表不能为空")
	}

	validEntities := make([]IDbEntity, 0, len(entities))
	for i, entity := range entities {
		if entity == nil {
			LogWarn("批量 UPSERT 跳过 nil 实体: 索引=%d", i)
			continue
		}
		validEntities = append(validEntities, entity)
	}
	if len(validEntities) == 0 {
		return NewValidationException("没有有效的实体可保存")
	}

	chunkSize := GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	groups := groupEntitiesByTable(validEntities, r.getTableName)
	for _, group := range groups {
		if err := r.saveBatchUpsertWithJournal(group, chunkSize); err != nil {
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
	r.testHookMu.Lock()
	hook := r.testUpsertHook
	r.testHookMu.Unlock()
	if hook != nil {
		return hook(entities)
	}
	return r.SaveBatchUpsert(entities)
}

func (r *BaseCrudRepository) saveBatchUpsertWithJournal(validEntities []IDbEntity, chunkSize int) error {
	if r.writeJournal != nil {
		for start := 0; start < len(validEntities); start += chunkSize {
			end := start + chunkSize
			if end > len(validEntities) {
				end = len(validEntities)
			}
			chunk := validEntities[start:end]
			entries, err := r.writeJournal.AppendEntities("SaveBatchUpsert", chunk)
			if err != nil {
				return err
			}
			if err := r.saveBatchUpsertOnce(chunk); err != nil {
				r.recordFailedBatchUpsert(chunk, err)
				return err
			}
			ids := make([]string, len(entries))
			for i, e := range entries {
				ids[i] = e.ID
			}
			if err := r.writeJournal.RemoveEntries(ids); err != nil {
				LogError("WAL 删除已成功条目失败: %v", err)
			}
		}
		return nil
	}
	return r.saveBatchUpsertChunked(validEntities, chunkSize)
}

func (r *BaseCrudRepository) saveBatchUpsertChunked(validEntities []IDbEntity, chunkSize int) error {
	if chunkSize <= 0 {
		chunkSize = GetCrudPerformanceSettings().Snapshot().BatchUpsertChunkSize
	}
	for start := 0; start < len(validEntities); start += chunkSize {
		end := start + chunkSize
		if end > len(validEntities) {
			end = len(validEntities)
		}
		if err := r.saveBatchUpsertOnce(validEntities[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (r *BaseCrudRepository) saveBatchUpsertOnce(validEntities []IDbEntity) error {
	if len(validEntities) == 0 {
		return nil
	}
	if r.db == nil || r.db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}

	LogDebug("开始批量 UPSERT: 实体数量=%d", len(validEntities))

	firstEntity := validEntities[0]
	tableName := r.getTableName(firstEntity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	for _, entity := range validEntities {
		entity.SerializeBeforeSaveDb()
	}

	firstFields := r.getFields(firstEntity)
	if len(firstFields) == 0 {
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段，请检查字段是否包含 db 标签", firstEntity))
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
		return NewValidationException(fmt.Sprintf("表 %s 没有可插入的字段", tableName))
	}

	hasPrimaryKey := false
	for _, col := range columns {
		if col == uidColumn {
			hasPrimaryKey = true
			break
		}
	}
	if !hasPrimaryKey && isAutoIncrement {
		return r.saveBatchInsertOnce(validEntities)
	}
	if !hasPrimaryKey {
		return NewValidationException(fmt.Sprintf("批量 UPSERT 要求主键 %s 有有效值", uidColumn))
	}

	for _, entity := range validEntities {
		pkValue := cm.GetPrimaryKeyValue(entity)
		if r.isZeroValue(pkValue) {
			return NewValidationException(fmt.Sprintf("批量 UPSERT 要求所有实体主键 %s 非零值", uidColumn))
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
		updateParts := make([]string, 0, len(columns))
		for _, col := range columns {
			if col != uidColumn {
				updateParts = append(updateParts, col+" = VALUES("+col+")")
			}
		}
		sql = appendBatchUpsertSQL(tableName, columns, placeholders, updateParts)
	} else {
		updateParts := make([]string, 0)
		for _, col := range columns {
			if col != uidColumn {
				updateParts = append(updateParts, col+" = VALUES("+col+")")
			}
		}
		if len(updateParts) > 0 {
			sql = "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES " +
				StringUtilsInstance.Join(placeholders, ",") + " ON DUPLICATE KEY UPDATE " + StringUtilsInstance.Join(updateParts, ", ")
		} else {
			sql = "INSERT IGNORE INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES " +
				StringUtilsInstance.Join(placeholders, ",")
		}
	}

	LogDebug("执行批量 UPSERT: 表=%s, 记录数=%d, 字段数=%d", tableName, len(validEntities), len(columns))

	result, err := r.db.execContext(context.Background(), sql, allValues...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%v", tableName, err)
			firstUidValue := cm.GetPrimaryKeyValue(firstEntity)
			r.recordFailedOperation("SaveBatchUpsert", tableName, sql, allValues, firstUidValue)
			if r.db.FaultTolerantMgr != nil {
				r.db.FaultTolerantMgr.CheckAndReconnect()
			}
			return NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("批量 UPSERT 失败: 表=%s, 错误=%v, SQL=%s", tableName, err, sql)
		return NewQueryExceptionWithCause(err, fmt.Sprintf("批量 UPSERT 到表 %s 失败", tableName))
	}

	rowsAffected, _ := result.RowsAffected()
	LogDebug("批量 UPSERT 完成: 表=%s, 影响行数=%d, 记录数=%d", tableName, rowsAffected, len(validEntities))
	return nil
}

// SaveBuffered 异步入队保存；缓冲满或未启用时同步 Save。
func (r *BaseCrudRepository) SaveBuffered(entity IDbEntity) error {
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
	r.wbMu.Lock()
	wb := r.writeBuffer
	r.wbMu.Unlock()
	if wb == nil {
		return nil
	}
	return wb.Flush()
}

func (r *BaseCrudRepository) ensureWriteBuffer(settings CrudPerformanceSettings) (*WriteBuffer, error) {
	r.wbMu.Lock()
	defer r.wbMu.Unlock()
	if r.writeBuffer == nil {
		r.writeBuffer = newWriteBuffer(r)
		r.writeBuffer.Start(settings)
	}
	return r.writeBuffer, nil
}

func (r *BaseCrudRepository) DeleteById(id any, entityType IDbEntity) error {
	// 参数验证
	if entityType == nil {
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

	sql := "DELETE FROM " + tableName + " WHERE " + uidColumn + " = ?"
	LogDebug("执行 DELETE: 表=%s, 主键列=%s, ID=%v, SQL=%s", tableName, uidColumn, id, sql)

	result, err := r.db.execContext(context.Background(), sql, id)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%v", tableName, err)
			r.recordFailedOperation("Delete", tableName, sql, []any{id}, id)
			if r.db.FaultTolerantMgr != nil {
				r.db.FaultTolerantMgr.CheckAndReconnect()
			}
			return NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("删除实体失败: 表=%s, ID=%v, 错误=%v, SQL=%s", tableName, id, err, sql)
		return NewQueryExceptionWithCause(err, fmt.Sprintf("删除表 %s 中 ID=%v 的记录失败", tableName, id))
	}

	affectedRows, _ := result.RowsAffected()
	if affectedRows == 0 {
		LogWarn("删除无影响: 表=%s, ID=%v, 可能记录不存在", tableName, id)
	} else {
		LogDebug("删除成功: 表=%s, ID=%v, 影响行数=%d", tableName, id, affectedRows)
	}

	return nil
}

func (r *BaseCrudRepository) FindById(id any, entityType IDbEntity) (IDbEntity, error) {
	// 参数验证
	if entityType == nil {
		return nil, NewValidationException("实体类型不能为 nil")
	}
	if id == nil {
		return nil, NewValidationException("查询ID不能为 nil")
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return nil, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 使用自动扫描获取唯一ID列名
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entityType)
	if uidColumn == "" {
		uidColumn = "id"
	}

	var sql string
	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnableSqlTemplateCache {
		sql = GetSqlTemplateCache().GetFindByIdSQL(entityType, tableName, uidColumn)
	} else {
		sql = "SELECT * FROM " + tableName + " WHERE " + uidColumn + " = ?"
	}
	LogDebug("执行查询: 表=%s, 主键列=%s, ID=%v, SQL=%s", tableName, uidColumn, id, sql)

	results := r.db.ExecuteQuery(sql, [][]any{{id}}, entityType)
	if len(results) > 0 {
		// 返回指针类型
		result := results[0]
		v := reflect.ValueOf(result)
		if v.Kind() != reflect.Ptr {
			// 如果不是指针，创建一个指针
			ptr := reflect.New(v.Type())
			ptr.Elem().Set(v)
			result = ptr.Interface()
		}
		// 类型断言为 IDbEntity
		if dbEntity, ok := result.(IDbEntity); ok {
			// 调用加载后的反序列化钩子
			dbEntity.DeserializeAfterLoadDb()
			LogDebug("查询成功: 表=%s, ID=%v, 找到记录", tableName, id)
			return dbEntity, nil
		}
		LogError("查询结果类型错误: 表=%s, ID=%v, 结果类型=%T, 未实现 IDbEntity 接口", tableName, id, result)
		return nil, NewDb233Exception(fmt.Sprintf("查询结果未实现 IDbEntity 接口，实际类型: %T", result))
	}

	LogDebug("查询无结果: 表=%s, ID=%v, 未找到记录", tableName, id)
	return nil, nil
}

// FindByIds 根据主键列表批量查找（单表 IN 查询）。
func (r *BaseCrudRepository) FindByIds(ids []any, entityType IDbEntity) ([]IDbEntity, error) {
	if entityType == nil {
		return nil, NewValidationException("实体类型不能为 nil")
	}
	if len(ids) == 0 {
		return []IDbEntity{}, nil
	}

	validIds := make([]any, 0, len(ids))
	for i, id := range ids {
		if id == nil {
			LogWarn("FindByIds 跳过 nil ID: 索引=%d", i)
			continue
		}
		validIds = append(validIds, id)
	}
	if len(validIds) == 0 {
		return []IDbEntity{}, nil
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return nil, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entityType)
	if uidColumn == "" {
		uidColumn = "id"
	}

	allEntities := make([]IDbEntity, 0, len(validIds))
	chunkSize := GetCrudPerformanceSettings().Snapshot().FindByIdsChunkSize
	for start := 0; start < len(validIds); start += chunkSize {
		end := start + chunkSize
		if end > len(validIds) {
			end = len(validIds)
		}
		chunk := validIds[start:end]

		var sql string
		if EnableAllocPoolEnabled() {
			sql = appendFindByIdsSQL(tableName, uidColumn, len(chunk))
		} else {
			placeholders := make([]string, len(chunk))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			sql = "SELECT * FROM " + tableName + " WHERE " + uidColumn + " IN (" + StringUtilsInstance.Join(placeholders, ",") + ")"
		}
		LogDebug("执行 FindByIds: 表=%s, 主键列=%s, ID数=%d, SQL=%s", tableName, uidColumn, len(chunk), sql)

		results := r.db.ExecuteQuery(sql, [][]any{chunk}, entityType)
		for i, result := range results {
			if dbEntity, ok := result.(IDbEntity); ok {
				dbEntity.DeserializeAfterLoadDb()
				allEntities = append(allEntities, dbEntity)
			} else {
				LogWarn("FindByIds 结果类型错误: 表=%s, 索引=%d, 结果类型=%T", tableName, i, result)
			}
		}
	}

	LogDebug("FindByIds 完成: 表=%s, 请求ID数=%d, 找到记录数=%d", tableName, len(validIds), len(allEntities))
	return allEntities, nil
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
		if entity == nil {
			continue
		}
		pk := cm.GetPrimaryKeyValue(entity)
		result[pk] = entity
	}
	return result, nil
}

func (r *BaseCrudRepository) FindAll(entityType IDbEntity) ([]IDbEntity, error) {
	// 参数验证
	if entityType == nil {
		return nil, NewValidationException("实体类型不能为 nil")
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return nil, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	sql := "SELECT * FROM " + tableName
	LogDebug("执行查询所有: 表=%s, SQL=%s", tableName, sql)

	results := r.db.ExecuteQuery(sql, [][]any{}, entityType)

	// 转换为 IDbEntity 切片并调用反序列化钩子
	entities := make([]IDbEntity, 0, len(results))
	for i, result := range results {
		if dbEntity, ok := result.(IDbEntity); ok {
			// 调用加载后的反序列化钩子
			dbEntity.DeserializeAfterLoadDb()
			entities = append(entities, dbEntity)
		} else {
			LogWarn("查询结果类型错误: 表=%s, 索引=%d, 结果类型=%T, 未实现 IDbEntity 接口", tableName, i, result)
		}
	}

	LogDebug("查询所有完成: 表=%s, 找到记录数=%d", tableName, len(entities))
	return entities, nil
}

func (r *BaseCrudRepository) FindByCondition(condition string, params []any, entityType IDbEntity) ([]IDbEntity, error) {
	// 参数验证
	if entityType == nil {
		return nil, NewValidationException("实体类型不能为 nil")
	}
	if condition == "" {
		return nil, NewValidationException("查询条件不能为空")
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return nil, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	sql := "SELECT * FROM " + tableName + " WHERE " + condition
	LogDebug("执行条件查询: 表=%s, 条件=%s, 参数数=%d, SQL=%s", tableName, condition, len(params), sql)

	results := r.db.ExecuteQuery(sql, [][]any{params}, entityType)

	// 转换为 IDbEntity 切片并调用反序列化钩子
	entities := make([]IDbEntity, 0, len(results))
	for i, result := range results {
		if dbEntity, ok := result.(IDbEntity); ok {
			// 调用加载后的反序列化钩子
			dbEntity.DeserializeAfterLoadDb()
			entities = append(entities, dbEntity)
		} else {
			LogWarn("查询结果类型错误: 表=%s, 索引=%d, 结果类型=%T, 未实现 IDbEntity 接口", tableName, i, result)
		}
	}

	LogDebug("条件查询完成: 表=%s, 找到记录数=%d", tableName, len(entities))
	return entities, nil
}

func (r *BaseCrudRepository) Update(entity IDbEntity) error {
	// 参数验证
	if entity == nil {
		return NewValidationException("实体不能为 nil")
	}

	// 调用保存前的序列化钩子
	entity.SerializeBeforeSaveDb()

	// 获取表名
	tableName := r.getTableName(entity)
	if tableName == "" {
		return NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	// 获取字段
	fields := r.getFields(entity)
	if len(fields) == 0 {
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段", entity))
	}

	// 使用自动扫描获取唯一ID列名
	cm := GetCrudManagerInstance()
	uidColumn := cm.GetPrimaryKeyColumnName(entity)
	if uidColumn == "" {
		uidColumn = "id"
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

	for name, value := range fields {
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
	LogDebug("执行 UPDATE: 表=%s, 主键列=%s, ID=%v, 更新字段数=%d, SQL=%s", tableName, uidColumn, id, len(setParts), sql)

	result, err := r.db.execContext(context.Background(), sql, values...)
	if err != nil {
		if isConnectionError(err) {
			LogWarn("数据库连接已关闭或不可用: 表=%s, 错误=%v", tableName, err)
			r.recordFailedOperation("Update", tableName, sql, values, id)
			if r.db.FaultTolerantMgr != nil {
				r.db.FaultTolerantMgr.CheckAndReconnect()
			}
			return NewQueryExceptionWithCause(err, "数据库连接已关闭或不可用，请检查网络连接")
		}
		LogError("更新实体失败: 表=%s, ID=%v, 错误=%v, SQL=%s", tableName, id, err, sql)
		return NewQueryExceptionWithCause(err, fmt.Sprintf("更新表 %s 中 ID=%v 的记录失败", tableName, id))
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		LogWarn("更新无影响: 表=%s, ID=%v, 可能记录不存在", tableName, id)
	} else {
		LogDebug("更新成功: 表=%s, ID=%v, 影响行数=%d", tableName, id, rowsAffected)
	}

	return nil
}

func (r *BaseCrudRepository) UpdateBatch(entities []IDbEntity) error {
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
	if entityType == nil {
		return 0, NewValidationException("实体类型不能为 nil")
	}

	tableName := r.getTableName(entityType)
	if tableName == "" {
		return 0, NewValidationException("无法获取表名，请确保实体实现了 TableName() 方法并返回非空字符串")
	}

	sql := "SELECT COUNT(*) FROM " + tableName
	LogDebug("执行计数查询: 表=%s, SQL=%s", tableName, sql)

	var count int64
	err := r.db.DataSource.QueryRow(sql).Scan(&count)
	if err != nil {
		LogError("计数查询失败: 表=%s, 错误=%v, SQL=%s", tableName, err, sql)
		return 0, NewQueryExceptionWithCause(err, fmt.Sprintf("统计表 %s 的记录数失败", tableName))
	}

	LogDebug("计数成功: 表=%s, 总数=%d", tableName, count)
	return count, nil
}

// 记录失败操作（连接异常时）
func (r *BaseCrudRepository) recordFailedOperation(operation string, tableName string, sql string, params []any, primaryKey any) {
	r.recordFailedOperationWithEntity(operation, tableName, sql, params, primaryKey, nil)
}

func (r *BaseCrudRepository) recordFailedOperationWithEntity(operation string, tableName string, sql string, params []any, primaryKey any, entity IDbEntity) {
	if r == nil || r.db == nil || r.db.FaultTolerantMgr == nil {
		return
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
		if data, err := SerializeEntity(entity); err == nil {
			op.EntityJSON = data
		}
	}
	r.db.FaultTolerantMgr.RecordFailedOperation(op)
}

func (r *BaseCrudRepository) recordFailedBatchUpsert(entities []IDbEntity, cause error) {
	for _, entity := range entities {
		if entity == nil {
			continue
		}
		tableName := r.getTableName(entity)
		pk := GetCrudManagerInstance().GetPrimaryKeyValue(entity)
		r.recordFailedOperationWithEntity("SaveBatchUpsert", tableName, "", nil, pk, entity)
	}
	if cause != nil {
		LogWarn("批量 UPSERT 失败已记录到容错队列: count=%d, err=%v", len(entities), cause)
	}
}
