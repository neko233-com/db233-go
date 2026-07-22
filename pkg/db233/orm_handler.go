package db233

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// OrmHandler 是 ORM 处理类，对应 Java 版本的 OrmHandler，使用反射将数据库结果映射到结构体。
type OrmHandler struct{}

// OrmBatch 批量 ORM 映射。
// rows: 数据库结果集。
// returnType: 返回类型。
// 返回: 映射后的对象列表。
func (o *OrmHandler) OrmBatch(rows *sql.Rows, returnType any) (results []any) {
	if rows == nil {
		LogError("ORM 查询结果集为 nil")
		return nil
	}
	defer func() {
		if rowsErr := rows.Err(); rowsErr != nil {
			LogError("ORM 行遍历失败: %s", safeErrorForLog(rowsErr))
		}
		if closeErr := rows.Close(); closeErr != nil {
			LogError("关闭 ORM 查询结果集失败: %s", safeErrorForLog(closeErr))
		}
	}()

	structType := reflect.TypeOf(returnType)
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}

	columns, err := rows.Columns()
	if err != nil {
		log.Printf("获取列名失败: %s", safeErrorForLog(err))
		return results
	}

	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnableFastOrmScan {
		if plan, planErr := GetOrmScanPlanCache().GetPlan(returnType, columns); planErr == nil {
			return o.ormBatchFast(rows, plan)
		}
	}

	return o.ormBatchLegacy(rows, structType, columns)
}

// OrmBatchStrict 是 OrmBatch 的 all-or-error 公开入口。
func (o *OrmHandler) OrmBatchStrict(rows *sql.Rows, returnType any) ([]any, error) {
	return o.ormBatchStrict(rows, returnType)
}

// ormBatchStrict 执行严格 ORM 映射，并独占 rows 的关闭责任。
// 任一读取、映射或关闭错误都会使结果整体失败，禁止交付部分数据。
func (o *OrmHandler) ormBatchStrict(rows *sql.Rows, returnType any) (results []any, err error) {
	if rows == nil {
		return nil, NewValidationException("查询结果集不能为 nil")
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			wrappedCloseErr := NewQueryExceptionWithCause(closeErr, "关闭查询结果集失败")
			results = nil
			if err == nil {
				err = wrappedCloseErr
			} else {
				err = errors.Join(err, wrappedCloseErr)
			}
		}
	}()

	structType, typeErr := strictOrmStructType(returnType)
	if typeErr != nil {
		return nil, typeErr
	}

	columns, columnsErr := rows.Columns()
	if columnsErr != nil {
		return nil, NewQueryExceptionWithCause(columnsErr, "获取严格查询列信息失败")
	}
	if len(columns) == 0 {
		return nil, NewQueryException("严格查询结果没有列信息")
	}

	settings := GetCrudPerformanceSettings().Snapshot()
	if settings.EnableFastOrmScan {
		planPrototype := reflect.New(structType).Interface()
		plan, planErr := GetOrmScanPlanCache().GetStrictPlan(planPrototype, columns)
		if planErr != nil {
			return nil, NewQueryExceptionWithCause(planErr, "构建严格 ORM 扫描计划失败")
		}
		return o.ormBatchFastStrict(rows, plan)
	}

	return o.ormBatchLegacyStrict(rows, structType, columns)
}

// ormBatchCompatibleStrict 保留 legacy ORM 的宽松数值转换（例如 DECIMAL ->
// float64），但严格传播 Query/Scan/Rows/Close/转换错误。兼容入口因此不会因
// 精确浮点策略破坏既有业务，同时也不再把驱动故障伪装成“未找到”。
func (o *OrmHandler) ormBatchCompatibleStrict(rows *sql.Rows, returnType any) (results []any, err error) {
	if rows == nil {
		return nil, NewValidationException("查询结果集不能为 nil")
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			wrappedCloseErr := NewQueryExceptionWithCause(closeErr, "关闭查询结果集失败")
			results = nil
			if err == nil {
				err = wrappedCloseErr
			} else {
				err = errors.Join(err, wrappedCloseErr)
			}
		}
	}()

	structType, typeErr := strictOrmStructType(returnType)
	if typeErr != nil {
		return nil, typeErr
	}
	columns, columnsErr := rows.Columns()
	if columnsErr != nil {
		return nil, NewQueryExceptionWithCause(columnsErr, "获取查询列信息失败")
	}
	if len(columns) == 0 {
		return nil, NewQueryException("查询结果没有列")
	}
	return o.ormBatchLegacyCompatibleChecked(rows, structType, columns)
}

func strictOrmStructType(returnType any) (reflect.Type, error) {
	if returnType == nil {
		return nil, NewValidationException("严格查询返回类型不能为 nil")
	}

	structType := reflect.TypeOf(returnType)
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return nil, NewValidationException(fmt.Sprintf("严格查询返回类型必须是 struct 或 *struct，实际类型: %T", returnType))
	}
	return structType, nil
}

func (o *OrmHandler) ormBatchLegacy(rows *sql.Rows, structType reflect.Type, columns []string) []any {
	var results []any
	scratch := acquireScanScratch(len(columns))
	defer releaseScanScratch(scratch)

	for rows.Next() {
		newInstancePtr := reflect.New(structType)
		newInstance := newInstancePtr.Elem()

		dest := scratch.dest
		for i := range dest {
			dest[i] = scratch.discardPtr(i)
		}

		err := rows.Scan(dest...)
		if err != nil {
			log.Printf("扫描行失败: %s", safeErrorForLog(err))
			continue
		}

		for i, col := range columns {
			field := o.findFieldByColumnName(newInstance, structType, col)
			if field.IsValid() && field.CanSet() {
				val := reflect.ValueOf(*scratch.discardPtr(i))
				if val.IsValid() {
					convertedVal, err := o.convertValue(val, field.Type())
					if err != nil {
						LogDebug("字段类型转换警告: 列=%s, 源类型=%s, 目标类型=%s, 错误=%s", col, val.Type(), field.Type(), safeErrorForLog(err))
						continue
					}
					field.Set(convertedVal)
				}
			}
		}

		results = append(results, newInstancePtr.Interface())
	}

	return results
}

func (o *OrmHandler) ormBatchLegacyStrict(rows *sql.Rows, structType reflect.Type, columns []string) ([]any, error) {
	results := make([]any, 0)
	scratch := acquireScanScratch(len(columns))
	defer releaseScanScratch(scratch)

	// Legacy mode avoids the global scan-plan cache, but column mapping itself is
	// invariant for the whole result set. Resolve it once instead of once per row.
	fieldPaths := make([][]int, len(columns))
	conversionOptions := make([]strictConversionOptions, len(columns))
	for i, column := range columns {
		path, ok := findOrmFieldPathByColumnName(structType, column)
		if !ok {
			continue
		}
		fieldPaths[i] = path
		conversionOptions[i] = strictConversionOptionsFor(fieldTypeByPath(structType, path))
	}

	rowIndex := 0
	for rows.Next() {
		newInstancePtr := reflect.New(structType)
		newInstance := newInstancePtr.Elem()

		dest := scratch.dest
		for i := range dest {
			dest[i] = scratch.discardPtr(i)
		}

		if scanErr := rows.Scan(dest...); scanErr != nil {
			return nil, NewQueryExceptionWithCause(scanErr, fmt.Sprintf("严格 ORM 扫描失败: row=%d", rowIndex))
		}

		for i, column := range columns {
			path := fieldPaths[i]
			if path == nil {
				continue
			}
			field, fieldErr := strictFieldByIndexPath(newInstance, path)
			if fieldErr != nil || !field.CanSet() {
				if fieldErr == nil {
					fieldErr = fmt.Errorf("字段不可设置")
				}
				return nil, NewQueryExceptionWithCause(
					fieldErr,
					fmt.Sprintf("严格 ORM 字段定位失败: row=%d, column=%s, path=%v", rowIndex, column, path),
				)
			}

			converted, convertErr := o.convertValueStrictWithOptions(
				*scratch.discardPtr(i), field.Type(), conversionOptions[i],
			)
			if convertErr != nil {
				return nil, NewQueryExceptionWithCause(
					convertErr,
					fmt.Sprintf("严格 ORM 字段转换失败: row=%d, column=%s, target=%s", rowIndex, column, field.Type()),
				)
			}
			field.Set(converted)
		}

		results = append(results, newInstancePtr.Interface())
		rowIndex++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, NewQueryExceptionWithCause(rowsErr, fmt.Sprintf("严格 ORM 行遍历失败: next_row=%d", rowIndex))
	}
	return results, nil
}

func (o *OrmHandler) ormBatchLegacyCompatibleChecked(rows *sql.Rows, structType reflect.Type, columns []string) ([]any, error) {
	results := make([]any, 0)
	scratch := acquireScanScratch(len(columns))
	defer releaseScanScratch(scratch)

	fieldPaths := make([][]int, len(columns))
	for i, column := range columns {
		path, ok := findOrmFieldPathByColumnName(structType, column)
		if ok {
			fieldPaths[i] = path
		}
	}

	rowIndex := 0
	for rows.Next() {
		newInstancePtr := reflect.New(structType)
		newInstance := newInstancePtr.Elem()
		dest := scratch.dest
		for i := range dest {
			dest[i] = scratch.discardPtr(i)
		}
		if scanErr := rows.Scan(dest...); scanErr != nil {
			return nil, NewQueryExceptionWithCause(scanErr, fmt.Sprintf("ORM 扫描失败: row=%d", rowIndex))
		}

		for i, column := range columns {
			path := fieldPaths[i]
			if path == nil {
				continue
			}
			field, fieldErr := strictFieldByIndexPath(newInstance, path)
			if fieldErr != nil || !field.CanSet() {
				if fieldErr == nil {
					fieldErr = fmt.Errorf("字段不可设置")
				}
				return nil, NewQueryExceptionWithCause(
					fieldErr,
					fmt.Sprintf("ORM 字段定位失败: row=%d, column=%s, path=%v", rowIndex, column, path),
				)
			}
			converted, convertErr := o.convertValue(reflect.ValueOf(*scratch.discardPtr(i)), field.Type())
			if convertErr != nil {
				return nil, NewQueryExceptionWithCause(
					convertErr,
					fmt.Sprintf("ORM 字段转换失败: row=%d, column=%s, target=%s", rowIndex, column, field.Type()),
				)
			}
			field.Set(converted)
		}
		results = append(results, newInstancePtr.Interface())
		rowIndex++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, NewQueryExceptionWithCause(rowsErr, fmt.Sprintf("ORM 行遍历失败: next_row=%d", rowIndex))
	}
	return results, nil
}

// findFieldByColumnName 根据列名查找字段，支持嵌入结构体递归查找。
// structValue: 结构体值。
// structType: 结构体类型。
// columnName: 列名。
// 返回: 找到的字段值。
func (o *OrmHandler) findFieldByColumnName(structValue reflect.Value, structType reflect.Type, columnName string) reflect.Value {
	// 首先尝试直接匹配字段名
	field := structValue.FieldByName(columnName)
	if field.IsValid() && field.CanSet() {
		return field
	}

	// 遍历所有字段，尝试通过 db 标签匹配或递归处理嵌入结构体
	for i := 0; i < structType.NumField(); i++ {
		structField := structType.Field(i)
		fieldValue := structValue.Field(i)

		// 处理嵌入结构体（Anonymous field）
		if structField.Anonymous {
			embeddedType := structField.Type
			embeddedValue := fieldValue

			// 如果是指针，需要解引用
			if embeddedType.Kind() == reflect.Ptr {
				if embeddedValue.IsNil() {
					// 如果是 nil 指针，创建新实例
					embeddedValue = reflect.New(embeddedType.Elem())
					fieldValue.Set(embeddedValue)
				}
				embeddedValue = embeddedValue.Elem()
				embeddedType = embeddedType.Elem()
			}

			// 如果是结构体，递归查找
			if embeddedType.Kind() == reflect.Struct {
				foundField := o.findFieldByColumnName(embeddedValue, embeddedType, columnName)
				if foundField.IsValid() && foundField.CanSet() {
					return foundField
				}
			}
			continue
		}

		// 检查 db 标签
		tag := structField.Tag.Get("db")
		if tag != "" {
			// 解析标签，获取列名（标签格式：column_name,options...）
			tagParts := strings.Split(tag, ",")
			dbColumnName := strings.TrimSpace(tagParts[0])

			// 忽略 db:"-" 标记的字段
			if dbColumnName == "-" {
				continue
			}

			// 匹配列名
			if dbColumnName == columnName {
				if fieldValue.CanSet() {
					return fieldValue
				}
			}
		}
	}

	// 未找到匹配字段
	return reflect.Value{}
}

// OrmSingle 单行 ORM 映射。
// rows: 数据库结果集。
// returnType: 返回类型。
// 返回: 映射后的对象。
func (o *OrmHandler) OrmSingle(rows *sql.Rows, returnType any) any {
	results := o.OrmBatch(rows, returnType)
	if len(results) > 0 {
		return results[0]
	}
	return nil
}

// convertValue 将数据库值转换为目标类型。
// 处理 MySQL 返回的 []uint8 (byte array) 到各种 Go 类型的转换。
func (o *OrmHandler) convertValue(sourceVal reflect.Value, targetType reflect.Type) (reflect.Value, error) {
	// 如果源值是 nil，返回零值
	if !sourceVal.IsValid() || (sourceVal.Kind() == reflect.Interface && sourceVal.IsNil()) {
		return reflect.Zero(targetType), nil
	}

	// 处理 any 包装
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

	// database/sql 驱动通常把 MySQL TINYINT(1) 扫描成 int64，而 Go 不允许
	// 直接 reflect.Convert 到 bool。兼容 ORM 入口保持 0=false、非 0=true；
	// 严格 ORM 入口仍由 convertValueStrict 执行精确策略。
	if targetType.Kind() == reflect.Bool {
		switch sourceVal.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(sourceVal.Int() != 0).Convert(targetType), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return reflect.ValueOf(sourceVal.Uint() != 0).Convert(targetType), nil
		case reflect.Float32, reflect.Float64:
			return reflect.ValueOf(sourceVal.Float() != 0).Convert(targetType), nil
		}
	}

	// 特殊处理：[]uint8 (MySQL byte array) 转换。
	// 指针目标必须先递归到元素类型，才能正确处理 MySQL 常见的 []byte -> *T。
	if sourceVal.Kind() == reflect.Slice && sourceVal.Type().Elem().Kind() == reflect.Uint8 {
		return o.convertFromBytes(sourceVal.Interface().([]byte), targetType)
	}

	return reflect.Value{}, fmt.Errorf("无法转换类型: %s -> %s", sourceVal.Type(), targetType)
}

// convertFromBytes 从字节数组转换到目标类型。
func (o *OrmHandler) convertFromBytes(data []byte, targetType reflect.Type) (reflect.Value, error) {
	if len(data) == 0 {
		return o.emptyJSONValue(targetType), nil
	}

	str := string(data)

	switch targetType.Kind() {
	case reflect.String:
		return reflect.ValueOf(str).Convert(targetType), nil

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
				return reflect.ValueOf(i != 0).Convert(targetType), nil
			}
			return reflect.Value{}, fmt.Errorf("转换为 bool 失败: %w", err)
		}
		return reflect.ValueOf(b).Convert(targetType), nil

	case reflect.Struct:
		// 特殊处理：time.Time
		if targetType == reflect.TypeOf(time.Time{}) {
			t, err := o.parseTime(str)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("转换为 time.Time 失败: %w", err)
			}
			return reflect.ValueOf(t), nil
		}
		return o.unmarshalJSONValue(data, targetType)

	case reflect.Slice:
		// 特殊处理：[]byte
		if targetType.Elem().Kind() == reflect.Uint8 {
			return reflect.ValueOf(data).Convert(targetType), nil
		}
		return o.unmarshalJSONValue(data, targetType)

	case reflect.Map, reflect.Array:
		return o.unmarshalJSONValue(data, targetType)

	case reflect.Chan, reflect.Func:
		return reflect.Value{}, fmt.Errorf("不支持从 []byte 转换到复杂类型: %s", targetType)

	default:
		return reflect.Value{}, fmt.Errorf("未知的目标类型: %s", targetType)
	}
}

func (o *OrmHandler) unmarshalJSONValue(data []byte, targetType reflect.Type) (reflect.Value, error) {
	if strings.TrimSpace(string(data)) == "" {
		return o.emptyJSONValue(targetType), nil
	}
	target := reflect.New(targetType)
	if err := json.Unmarshal(data, target.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("JSON反序列化失败: %w", err)
	}
	value := target.Elem()
	if isNilContainer(value) {
		return o.emptyJSONValue(targetType), nil
	}
	return value, nil
}

func (o *OrmHandler) emptyJSONValue(targetType reflect.Type) reflect.Value {
	switch targetType.Kind() {
	case reflect.Map:
		return reflect.MakeMap(targetType)
	case reflect.Slice:
		return reflect.MakeSlice(targetType, 0, 0)
	case reflect.Ptr:
		elem := o.emptyJSONValue(targetType.Elem())
		ptr := reflect.New(targetType.Elem())
		ptr.Elem().Set(elem)
		return ptr
	default:
		return reflect.Zero(targetType)
	}
}

func isNilContainer(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Ptr, reflect.Interface:
		return value.IsNil()
	default:
		return false
	}
}

// parseTime 解析时间字符串。
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

// OrmHandlerInstance 是单例实例。
var OrmHandlerInstance = &OrmHandler{}
