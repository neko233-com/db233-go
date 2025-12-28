package db233

import (
	"database/sql"
	"reflect"
	"strings"
)

/**
 * CrudRepository - CRUD 存储库接口
 *
 * 提供基本的 CRUD 操作
 *
 * @author SolarisNeko
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
	 * 保存实体
	 */
	Save(entity interface{}) error

	/**
	 * 批量保存实体
	 */
	SaveBatch(entities []interface{}) error

	/**
	 * 根据主键删除
	 */
	DeleteById(id interface{}, entityType interface{}) error

	/**
	 * 根据主键查找
	 */
	FindById(id interface{}, entityType interface{}) (interface{}, error)

	/**
	 * 查找所有
	 */
	FindAll(entityType interface{}) ([]interface{}, error)

	/**
	 * 根据条件查找
	 */
	FindByCondition(condition string, params []interface{}, entityType interface{}) ([]interface{}, error)

	/**
	 * 更新实体
	 */
	Update(entity interface{}) error

	/**
	 * 批量更新
	 */
	UpdateBatch(entities []interface{}) error

	/**
	 * 统计数量
	 */
	Count(entityType interface{}) (int64, error)
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
func (r *BaseCrudRepository) Save(entity interface{}) error {
	// 简化实现：使用反射获取表名和字段
	tableName := r.getTableName(entity)
	fields := r.getFields(entity)

	// 构建 INSERT 语句
	columns := make([]string, 0, len(fields))
	placeholders := make([]string, 0, len(fields))
	values := make([]interface{}, 0, len(fields))

	for name, value := range fields {
		columns = append(columns, name)
		placeholders = append(placeholders, "?")
		values = append(values, value)
	}

	sql := "INSERT INTO " + tableName + " (" + StringUtilsInstance.Join(columns, ",") + ") VALUES (" + StringUtilsInstance.Join(placeholders, ",") + ")"

	result, err := r.db.DataSource.Exec(sql, values...)
	if err != nil {
		return err
	}

	// 处理自增主键
	lastInsertId, err := result.LastInsertId()
	if err == nil {
		r.setPrimaryKeyValue(entity, lastInsertId)
	}

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
 */
func (r *BaseCrudRepository) getTableName(entity interface{}) string {
	// 简化：使用类型名作为表名
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

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fieldValue := v.Field(i).Interface()

		// 解析 db 标签
		tag := field.Tag.Get("db")
		var columnName string
		if tag != "" {
			// 解析标签，获取列名（标签格式：column_name,options...）
			tagParts := strings.Split(tag, ",")
			columnName = strings.TrimSpace(tagParts[0])
		} else {
			// 如果没有标签，使用驼峰转下划线
			columnName = StringUtilsInstance.CamelToSnake(field.Name)
		}

		fields[columnName] = fieldValue
	}

	return fields
}

/**
 * 其他方法的简化实现
 */
func (r *BaseCrudRepository) SaveBatch(entities []interface{}) error {
	for _, entity := range entities {
		if err := r.Save(entity); err != nil {
			return err
		}
	}
	return nil
}

func (r *BaseCrudRepository) DeleteById(id interface{}, entityType interface{}) error {
	tableName := r.getTableName(entityType)
	sql := "DELETE FROM " + tableName + " WHERE id = ?"
	r.db.ExecuteOriginalUpdate(sql, [][]interface{}{{id}})
	return nil
}

func (r *BaseCrudRepository) FindById(id interface{}, entityType interface{}) (interface{}, error) {
	tableName := r.getTableName(entityType)
	sql := "SELECT * FROM " + tableName + " WHERE id = ?"
	results := r.db.ExecuteQuery(sql, [][]interface{}{{id}}, entityType)
	if len(results) > 0 {
		// 返回指针类型
		result := results[0]
		v := reflect.ValueOf(result)
		if v.Kind() != reflect.Ptr {
			// 如果不是指针，创建一个指针
			ptr := reflect.New(v.Type())
			ptr.Elem().Set(v)
			return ptr.Interface(), nil
		}
		return result, nil
	}
	return nil, nil
}

func (r *BaseCrudRepository) FindAll(entityType interface{}) ([]interface{}, error) {
	tableName := r.getTableName(entityType)
	sql := "SELECT * FROM " + tableName
	return r.db.ExecuteQuery(sql, [][]interface{}{}, entityType), nil
}

func (r *BaseCrudRepository) FindByCondition(condition string, params []interface{}, entityType interface{}) ([]interface{}, error) {
	tableName := r.getTableName(entityType)
	sql := "SELECT * FROM " + tableName + " WHERE " + condition
	return r.db.ExecuteQuery(sql, [][]interface{}{params}, entityType), nil
}

func (r *BaseCrudRepository) Update(entity interface{}) error {
	// 简化实现
	tableName := r.getTableName(entity)
	fields := r.getFields(entity)

	// 假设有 id 字段
	id, exists := fields["id"]
	if !exists {
		return NewDb233Exception("实体缺少 id 字段")
	}

	setParts := make([]string, 0)
	values := make([]interface{}, 0)

	for name, value := range fields {
		if name != "id" {
			setParts = append(setParts, name+" = ?")
			values = append(values, value)
		}
	}
	values = append(values, id)

	sql := "UPDATE " + tableName + " SET " + StringUtilsInstance.Join(setParts, ", ") + " WHERE id = ?"
	_, err := r.db.DataSource.Exec(sql, values...)
	return err
}

func (r *BaseCrudRepository) UpdateBatch(entities []interface{}) error {
	for _, entity := range entities {
		if err := r.Update(entity); err != nil {
			return err
		}
	}
	return nil
}

func (r *BaseCrudRepository) Count(entityType interface{}) (int64, error) {
	tableName := r.getTableName(entityType)
	sql := "SELECT COUNT(*) FROM " + tableName

	var count int64
	err := r.db.DataSource.QueryRow(sql).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}
