package db233

import (
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"
)

/**
 * OrmHandler - ORM 处理类
 *
 * 对应 Java 版本的 OrmHandler
 * 使用反射将数据库结果映射到结构体
 *
 * @author neko233-com
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
					// 处理类型转换（使用新的转换方法）
					convertedVal, err := o.convertValue(val, field.Type())
					if err != nil {
						LogDebug("字段类型转换警告: 列=%s, 源类型=%s, 目标类型=%s, 错误=%v", col, val.Type(), field.Type(), err)
						continue
					}
					field.Set(convertedVal)
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
 * convertValue 将数据库值转换为目标类型
 *
 * 处理 MySQL 返回的 []uint8 (byte array) 到各种 Go 类型的转换
 */
func (o *OrmHandler) convertValue(sourceVal reflect.Value, targetType reflect.Type) (reflect.Value, error) {
	// 如果源值是 nil，返回零值
	if !sourceVal.IsValid() || (sourceVal.Kind() == reflect.Interface && sourceVal.IsNil()) {
		return reflect.Zero(targetType), nil
	}

	// 处理 interface{} 包装
	if sourceVal.Kind() == reflect.Interface {
		sourceVal = sourceVal.Elem()
	}

	// 如果类型完全匹配，直接返回
	if sourceVal.Type() == targetType {
		return sourceVal, nil
	}

	// 如果可以直接转换，使用 Convert
	if sourceVal.Type().ConvertibleTo(targetType) {
		return sourceVal.Convert(targetType), nil
	}

	// 特殊处理：[]uint8 (MySQL byte array) 转换
	if sourceVal.Kind() == reflect.Slice && sourceVal.Type().Elem().Kind() == reflect.Uint8 {
		return o.convertFromBytes(sourceVal.Interface().([]byte), targetType)
	}

	// 处理指针类型
	if targetType.Kind() == reflect.Ptr {
		// 创建指针指向的类型的值
		elemType := targetType.Elem()
		elemVal, err := o.convertValue(sourceVal, elemType)
		if err != nil {
			return reflect.Value{}, err
		}
		ptrVal := reflect.New(elemType)
		ptrVal.Elem().Set(elemVal)
		return ptrVal, nil
	}

	return reflect.Value{}, fmt.Errorf("无法转换类型: %s -> %s", sourceVal.Type(), targetType)
}

/**
 * convertFromBytes 从字节数组转换到目标类型
 */
func (o *OrmHandler) convertFromBytes(data []byte, targetType reflect.Type) (reflect.Value, error) {
	if len(data) == 0 {
		return reflect.Zero(targetType), nil
	}

	str := string(data)

	switch targetType.Kind() {
	case reflect.String:
		return reflect.ValueOf(str), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("转换为 int 失败: %w", err)
		}
		return reflect.ValueOf(i).Convert(targetType), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(str, 10, 64)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("转换为 uint 失败: %w", err)
		}
		return reflect.ValueOf(u).Convert(targetType), nil

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("转换为 float 失败: %w", err)
		}
		return reflect.ValueOf(f).Convert(targetType), nil

	case reflect.Bool:
		b, err := strconv.ParseBool(str)
		if err != nil {
			// 尝试数字转换：0 = false, 非0 = true
			i, err2 := strconv.ParseInt(str, 10, 64)
			if err2 == nil {
				return reflect.ValueOf(i != 0), nil
			}
			return reflect.Value{}, fmt.Errorf("转换为 bool 失败: %w", err)
		}
		return reflect.ValueOf(b), nil

	case reflect.Struct:
		// 特殊处理：time.Time
		if targetType == reflect.TypeOf(time.Time{}) {
			t, err := o.parseTime(str)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("转换为 time.Time 失败: %w", err)
			}
			return reflect.ValueOf(t), nil
		}
		return reflect.Value{}, fmt.Errorf("不支持从 []byte 转换到结构体: %s", targetType)

	case reflect.Slice:
		// 特殊处理：[]byte
		if targetType.Elem().Kind() == reflect.Uint8 {
			return reflect.ValueOf(data), nil
		}
		return reflect.Value{}, fmt.Errorf("不支持从 []byte 转换到切片: %s", targetType)

	case reflect.Map, reflect.Array, reflect.Chan, reflect.Func:
		return reflect.Value{}, fmt.Errorf("不支持从 []byte 转换到复杂类型: %s", targetType)

	default:
		return reflect.Value{}, fmt.Errorf("未知的目标类型: %s", targetType)
	}
}

/**
 * parseTime 解析时间字符串
 */
func (o *OrmHandler) parseTime(str string) (time.Time, error) {
	// 常见的时间格式
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
		time.RFC3339,
		time.RFC3339Nano,
	}

	for _, format := range formats {
		t, err := time.Parse(format, str)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("无法解析时间字符串: %s", str)
}

/**
 * 单例实例
 */
var OrmHandlerInstance = &OrmHandler{}
