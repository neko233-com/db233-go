package db233

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

/**
 * CrudRepository - CRUD 存储库接口
 *
 * 提供基本的 CRUD 操作
 * 所有实体必须实现 IDbEntity 接口
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type CrudRepository interface {
	/**
	 * 获取绑定的数据源
	 */
	GetBindingDataSource() *sql.DB

	/**
	 * 获取数据库实例
	 */
	GetDb() *Db

	/**
	 * 保存实体（必须实现 IDbEntity 接口）
	 */
	Save(entity IDbEntity) error

	/**
	 * 批量保存实体（必须实现 IDbEntity 接口）
	 */
	SaveBatch(entities []IDbEntity) error

	/**
	 * 根据主键删除
	 */
	DeleteById(id interface{}, entityType IDbEntity) error

	/**
	 * 根据主键查找
	 */
	FindById(id interface{}, entityType IDbEntity) (IDbEntity, error)

	/**
	 * 查找所有
	 */
	FindAll(entityType IDbEntity) ([]IDbEntity, error)

	/**
	 * 根据条件查找
	 */
	FindByCondition(condition string, params []interface{}, entityType IDbEntity) ([]IDbEntity, error)

	/**
	 * 更新实体（必须实现 IDbEntity 接口）
	 */
	Update(entity IDbEntity) error

	/**
	 * 批量更新（必须实现 IDbEntity 接口）
	 */
	UpdateBatch(entities []IDbEntity) error

	/**
	 * 统计数量
	 */
	Count(entityType IDbEntity) (int64, error)
}

/**
 * BaseCrudRepository - 基础 CRUD 实现
 */
type BaseCrudRepository struct {
	db *Db
}

/**
 * 创建基础 CRUD 存储库
 */
func NewBaseCrudRepository(db *Db) *BaseCrudRepository {
	return &BaseCrudRepository{db: db}
}

/**
 * 获取绑定的数据源
 */
func (r *BaseCrudRepository) GetBindingDataSource() *sql.DB {
	return r.db.GetDataSource()
}

/**
 * 获取数据库实例
 */
func (r *BaseCrudRepository) GetDb() *Db {
	return r.db
}

/**
 * 保存实体
 */
func (r *BaseCrudRepository) Save(entity IDbEntity) error {
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
		return NewValidationException(fmt.Sprintf("实体 %T 没有可映射的字段，请检查字段是否包含 db 标签", entity))
	}

	// 获取唯一ID列名
	uidColumn := entity.GetDbUid()
	if uidColumn == "" {
		uidColumn = "id"
	}

	// 构建 INSERT 语句
	columns := make([]string, 0, len(fields))
	placeholders := make([]string, 0, len(fields))
	values := make([]interface{}, 0, len(fields))

	for name, value := range fields {
		// 跳过空字符串的主键字段（自增主键或空主键应该由数据库处理）
		if name == uidColumn {
			// 检查值是否为零值或空字符串
			if r.isZeroValue(value) {
				LogDebug("跳过空主键字段: 表=%s, 主键列=%s (值为空，将由数据库自动处理)", tableName, uidColumn)
				continue // 跳过空主键，让数据库自动处理（自增或默认值）
			}
		}
		
		// 检查值是否为 nil（额外检查）
		if value == nil {
			LogDebug("跳过 nil 值字段: 表=%s, 字段=%s", tableName, name)
			continue
		}

		columns = append(columns, name)
		placeholders = append(placeholders, "?")
		values = append(values, value)
	}

	if len(columns) == 0 {
		return NewValidationException(fmt.Sprintf("表 %s 没有可插入的字段（所有字段都为空或已跳过）", tableName))
	}

	sql := "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES (" + StringUtilsInstance.Join(placeholders, ",") + ")"

	LogDebug("执行 INSERT: 表=%s, 字段数=%d, SQL=%s", tableName, len(columns), sql)

	result, err := r.db.DataSource.Exec(sql, values...)
	if err != nil {
		LogError("保存实体失败: 表=%s, 错误=%v, SQL=%s", tableName, err, sql)
		return NewQueryExceptionWithCause(err, fmt.Sprintf("保存实体到表 %s 失败", tableName))
	}

	// 处理自增主键
	lastInsertId, err := result.LastInsertId()
	if err == nil && lastInsertId > 0 {
		r.setPrimaryKeyValue(entity, lastInsertId)
		LogDebug("自增主键已设置: 表=%s, 主键列=%s, 值=%d", tableName, uidColumn, lastInsertId)
	}

	rowsAffected, _ := result.RowsAffected()
	LogDebug("保存成功: 表=%s, 影响行数=%d", tableName, rowsAffected)

	return nil
}

/**
 * 设置主键值
 */
func (r *BaseCrudRepository) setPrimaryKeyValue(entity interface{}, id int64) {
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("db")
		if tag != "" {
			tagParts := strings.Split(tag, ",")
			for _, part := range tagParts {
				part = strings.TrimSpace(part)
				if part == "primary_key" || part == "auto_increment" {
					// 设置主键值
					fieldValue := v.Field(i)
					if fieldValue.CanSet() {
						switch fieldValue.Kind() {
						case reflect.Int, reflect.Int64:
							fieldValue.SetInt(id)
						case reflect.Int32:
							fieldValue.SetInt(id)
						}
					}
					return
				}
			}
		}
	}
}

/**
 * 获取表名
 *
 * @param entity 实现了 IDbEntity 接口的实体
 * @return string 表名
 */
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

/**
 * 获取字段
 */
func (r *BaseCrudRepository) getFields(entity interface{}) map[string]interface{} {
	v := reflect.ValueOf(entity)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	fields := make(map[string]interface{})
	t := v.Type()
	entityTypeName := t.Name()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i)

		// 检查字段是否可导出（可访问）
		if !fieldValue.CanInterface() {
			LogDebug("跳过未导出字段: 实体=%s, 字段=%s (字段未导出，无法访问)", entityTypeName, field.Name)
			continue
		}

		// 解析 db 标签
		tag := field.Tag.Get("db")
		var columnName string
		var shouldSkip bool

		if tag == "-" {
			// 明确标记为跳过
			LogDebug("跳过字段（db标签为'-'）: 实体=%s, 字段=%s", entityTypeName, field.Name)
			continue
		}

		if tag != "" {
			// 解析标签，获取列名（标签格式：column_name,options...）
			tagParts := strings.Split(tag, ",")
			columnName = strings.TrimSpace(tagParts[0])
			if columnName == "" {
				// 如果 db 标签为空（如 db:""），跳过该字段
				LogDebug("跳过字段（db标签为空）: 实体=%s, 字段=%s", entityTypeName, field.Name)
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
			// 如果没有标签（tag == ""），也跳过该字段（只有明确指定 db 标签的字段才会被处理）
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

	return fields
}

/**
 * 判断是否为复杂类型（需要序列化）
 */
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

/**
 * 序列化复杂类型为 JSON 字符串
 */
func (r *BaseCrudRepository) serializeComplexType(value interface{}, fieldType reflect.Type) (string, error) {
	// 如果值已经是字符串，直接返回
	if str, ok := value.(string); ok {
		return str, nil
	}

	// 如果值为 nil，返回空字符串
	if value == nil {
		return "", nil
	}

	// 使用 JSON 序列化
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("JSON序列化失败: %w", err)
	}

	return string(jsonBytes), nil
}

/**
 * 判断值是否为零值
 */
func (r *BaseCrudRepository) isZeroValue(value interface{}) bool {
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
			if !r.isZeroValue(fieldValue.Interface()) {
				return false
			}
		}
		return true
	}
	return false
}

/**
 * 其他方法的简化实现
 */
func (r *BaseCrudRepository) SaveBatch(entities []IDbEntity) error {
	// 参数验证
	if entities == nil {
		return NewValidationException("实体列表不能为 nil")
	}
	if len(entities) == 0 {
		return NewValidationException("实体列表不能为空")
	}

	LogDebug("开始批量保存: 实体数量=%d", len(entities))
	
	successCount := 0
	for i, entity := range entities {
		if entity == nil {
			LogWarn("批量保存跳过 nil 实体: 索引=%d", i)
			continue
		}
		
		if err := r.Save(entity); err != nil {
			LogError("批量保存失败: 索引=%d, 实体类型=%T, 错误=%v", i, entity, err)
			return NewQueryExceptionWithCause(err, fmt.Sprintf("批量保存失败，已成功保存 %d/%d 条记录，第 %d 条记录保存失败", successCount, len(entities), i+1))
		}
		successCount++
	}

	LogDebug("批量保存完成: 成功=%d, 总数=%d", successCount, len(entities))
	return nil
}

func (r *BaseCrudRepository) DeleteById(id interface{}, entityType IDbEntity) error {
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

	// 使用 GetDbUid 获取唯一ID列名，如果为空则使用默认的 "id"
	uidColumn := entityType.GetDbUid()
	if uidColumn == "" {
		uidColumn = "id"
	}

	sql := "DELETE FROM " + tableName + " WHERE " + uidColumn + " = ?"
	LogDebug("执行 DELETE: 表=%s, 主键列=%s, ID=%v, SQL=%s", tableName, uidColumn, id, sql)

	affectedRows := r.db.ExecuteOriginalUpdate(sql, [][]interface{}{{id}})
	if affectedRows == 0 {
		LogWarn("删除无影响: 表=%s, ID=%v, 可能记录不存在", tableName, id)
	} else {
		LogDebug("删除成功: 表=%s, ID=%v, 影响行数=%d", tableName, id, affectedRows)
	}

	return nil
}

func (r *BaseCrudRepository) FindById(id interface{}, entityType IDbEntity) (IDbEntity, error) {
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

	// 使用 GetDbUid 获取唯一ID列名，如果为空则使用默认的 "id"
	uidColumn := entityType.GetDbUid()
	if uidColumn == "" {
		uidColumn = "id"
	}

	sql := "SELECT * FROM " + tableName + " WHERE " + uidColumn + " = ?"
	LogDebug("执行查询: 表=%s, 主键列=%s, ID=%v, SQL=%s", tableName, uidColumn, id, sql)

	results := r.db.ExecuteQuery(sql, [][]interface{}{{id}}, entityType)
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

	results := r.db.ExecuteQuery(sql, [][]interface{}{}, entityType)
	
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

func (r *BaseCrudRepository) FindByCondition(condition string, params []interface{}, entityType IDbEntity) ([]IDbEntity, error) {
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

	results := r.db.ExecuteQuery(sql, [][]interface{}{params}, entityType)
	
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

	// 使用 GetDbUid 获取唯一ID列名，如果为空则使用默认的 "id"
	uidColumn := entity.GetDbUid()
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
	values := make([]interface{}, 0)

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

	result, err := r.db.DataSource.Exec(sql, values...)
	if err != nil {
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
	// 参数验证
	if entities == nil {
		return NewValidationException("实体列表不能为 nil")
	}
	if len(entities) == 0 {
		return NewValidationException("实体列表不能为空")
	}

	LogDebug("开始批量更新: 实体数量=%d", len(entities))
	
	successCount := 0
	for i, entity := range entities {
		if entity == nil {
			LogWarn("批量更新跳过 nil 实体: 索引=%d", i)
			continue
		}
		
		if err := r.Update(entity); err != nil {
			LogError("批量更新失败: 索引=%d, 实体类型=%T, 错误=%v", i, entity, err)
			return NewQueryExceptionWithCause(err, fmt.Sprintf("批量更新失败，已成功更新 %d/%d 条记录，第 %d 条记录更新失败", successCount, len(entities), i+1))
		}
		successCount++
	}

	LogDebug("批量更新完成: 成功=%d, 总数=%d", successCount, len(entities))
	return nil
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

	LogDebug("计数查询成功: 表=%s, 记录数=%d", tableName, count)
	return count, nil
}
