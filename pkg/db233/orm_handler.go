package db233

import (
	"database/sql"
	"log"
	"reflect"
	"strings"
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
			// 首先尝试直接匹配字段名
			field := newInstance.FieldByName(col)
			if !field.IsValid() || !field.CanSet() {
				// 尝试通过标签匹配
				for j := 0; j < structType.NumField(); j++ {
					structField := structType.Field(j)
					tag := structField.Tag.Get("db")
					if tag != "" {
						// 解析标签，获取列名（标签格式：column_name,options...）
						tagParts := strings.Split(tag, ",")
						columnName := strings.TrimSpace(tagParts[0])
						if columnName == col {
							field = newInstance.Field(j)
							break
						}
					}
				}
			}

			if field.IsValid() && field.CanSet() {
				val := reflect.ValueOf(scanTargets[i]).Elem()
				if val.IsValid() {
					// 处理类型转换
					targetVal := val
					targetType := field.Type()

					// 如果类型不匹配，尝试转换
					if val.Type() != targetType {
						if val.CanConvert(targetType) {
							targetVal = val.Convert(targetType)
						} else {
							// 特殊处理：interface{} 转换为具体类型
							if val.Kind() == reflect.Interface && !val.IsNil() {
								innerVal := val.Elem()
								if innerVal.CanConvert(targetType) {
									targetVal = innerVal.Convert(targetType)
								} else {
									log.Printf("无法转换类型: %s -> %s", innerVal.Type(), targetType)
									continue
								}
							} else {
								log.Printf("无法转换类型: %s -> %s", val.Type(), targetType)
								continue
							}
						}
					}

					field.Set(targetVal)
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
