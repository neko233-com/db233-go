package db233

import (
	"database/sql"
	"log"
	"reflect"
)

/**
 * OrmHandler - ORM 处理类
 *
 * 对应 Java 版本的 OrmHandler
 * 使用反射将数据库结果映射到结构体
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type OrmHandler struct{}

/**
 * 批量 ORM 映射
 *
 * @param rows 数据库结果集
 * @param returnType 返回类型
 * @return []interface{} 映射后的对象列表
 */
func (o *OrmHandler) OrmBatch(rows *sql.Rows, returnType interface{}) []interface{} {
	defer rows.Close()

	var results []interface{}

	// 获取结构体类型
	structType := reflect.TypeOf(returnType)
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}

	// 获取列名
	columns, err := rows.Columns()
	if err != nil {
		log.Printf("获取列名失败: %v", err)
		return results
	}

	for rows.Next() {
		// 创建新实例
		newInstance := reflect.New(structType).Elem()

		// 准备扫描目标
		scanTargets := make([]interface{}, len(columns))
		for i := range scanTargets {
			scanTargets[i] = new(interface{})
		}

		// 扫描行
		err := rows.Scan(scanTargets...)
		if err != nil {
			log.Printf("扫描行失败: %v", err)
			continue
		}

		// 映射到结构体字段
		for i, col := range columns {
			field := newInstance.FieldByName(col)
			if field.IsValid() && field.CanSet() {
				val := reflect.ValueOf(scanTargets[i]).Elem()
				if val.IsValid() {
					field.Set(val)
				}
			}
		}

		results = append(results, newInstance.Interface())
	}

	return results
}

/**
 * 单行 ORM 映射
 *
 * @param rows 数据库结果集
 * @param returnType 返回类型
 * @return interface{} 映射后的对象
 */
func (o *OrmHandler) OrmSingle(rows *sql.Rows, returnType interface{}) interface{} {
	results := o.OrmBatch(rows, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return nil
}

/**
 * 单例实例
 */
var OrmHandlerInstance = &OrmHandler{}
