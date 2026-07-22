package db233

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// OrmScanPlan 预计算的列→字段扫描计划（FindById / OrmBatch 热路径）。
type OrmScanPlan struct {
	entityType reflect.Type
	columns    []string
	fieldPaths [][]int // 与 columns 对齐；nil 表示丢弃列
	indirect   []bool  // true 表示 Scan 到 any 再 convertValue
}

// OrmScanPlanCache 按 (实体类型 + 列签名) 缓存扫描计划。
type OrmScanPlanCache struct {
	mu    sync.RWMutex
	cache map[ormScanPlanCacheKey]*OrmScanPlan
}

type ormScanPlanCacheKey struct {
	entityType      reflect.Type
	columnSignature string
	strict          bool
}

var (
	ormScanPlanCacheInstance *OrmScanPlanCache
	ormScanPlanCacheOnce     sync.Once
)

// GetOrmScanPlanCache 获取扫描计划缓存单例。
func GetOrmScanPlanCache() *OrmScanPlanCache {
	ormScanPlanCacheOnce.Do(func() {
		ormScanPlanCacheInstance = &OrmScanPlanCache{
			cache: make(map[ormScanPlanCacheKey]*OrmScanPlan),
		}
	})
	return ormScanPlanCacheInstance
}

// GetPlan 获取或构建扫描计划。
func (c *OrmScanPlanCache) GetPlan(entityPrototype any, columns []string) (*OrmScanPlan, error) {
	return c.getPlan(entityPrototype, columns, false)
}

// GetStrictPlan 获取不依赖 IDbEntity/TableName 的严格扫描计划。
// 单独使用缓存模式，避免改变既有 Fast ORM 的 Entity metadata 映射语义。
func (c *OrmScanPlanCache) GetStrictPlan(entityPrototype any, columns []string) (*OrmScanPlan, error) {
	return c.getPlan(entityPrototype, columns, true)
}

func (c *OrmScanPlanCache) getPlan(entityPrototype any, columns []string, strict bool) (*OrmScanPlan, error) {
	if entityPrototype == nil || len(columns) == 0 {
		return nil, fmt.Errorf("invalid scan plan input")
	}
	entityType := reflect.TypeOf(entityPrototype)
	for entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	if entityType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("scan plan entity type must be struct, got %s", entityType)
	}
	key := ormScanPlanCacheKey{
		entityType:      entityType,
		columnSignature: strings.Join(columns, "\x00"),
		strict:          strict,
	}

	c.mu.RLock()
	if plan, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return plan, nil
	}
	c.mu.RUnlock()

	var metadata *EntityMetadata
	if !strict {
		var metadataErr error
		metadata, metadataErr = GetEntityMetadataCacheInstance().GetOrBuild(entityPrototype)
		if metadataErr != nil {
			return nil, metadataErr
		}
	}

	plan := &OrmScanPlan{
		entityType: entityType,
		columns:    append([]string(nil), columns...),
		fieldPaths: make([][]int, len(columns)),
		indirect:   make([]bool, len(columns)),
	}
	for i, col := range columns {
		var path []int
		var ok bool
		if strict {
			path, ok = findOrmFieldPathByColumnName(entityType, col)
		} else {
			path, ok = metadata.ColumnToFieldPath[col]
			if !ok {
				for column, candidate := range metadata.ColumnToFieldPath {
					if strings.EqualFold(column, col) {
						path = candidate
						ok = true
						break
					}
				}
			}
		}
		if ok {
			plan.fieldPaths[i] = append([]int(nil), path...)
			ft := fieldTypeByPath(entityType, path)
			plan.indirect[i] = !isDirectScannableType(ft)
		}
	}

	c.mu.Lock()
	c.cache[key] = plan
	c.mu.Unlock()
	return plan, nil
}

// findOrmFieldPathByColumnName 只基于结构体形状解析列路径，不依赖 IDbEntity/TableName。
// Fast 与严格 Legacy 共享此解析器，确保普通 DTO、嵌入字段和未知列的行为一致。
func findOrmFieldPathByColumnName(structType reflect.Type, columnName string) ([]int, bool) {
	for structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return nil, false
	}
	return findOrmFieldPathByColumnNameRecursive(structType, columnName, make(map[reflect.Type]bool))
}

func findOrmFieldPathByColumnNameRecursive(
	structType reflect.Type,
	columnName string,
	visiting map[reflect.Type]bool,
) ([]int, bool) {
	if visiting[structType] {
		return nil, false
	}
	visiting[structType] = true
	defer delete(visiting, structType)

	// 保留 Legacy 的字段名优先级，同时尊重 db:"-"。
	if field, ok := structType.FieldByName(columnName); ok && field.IsExported() && !ormFieldIgnored(field) {
		return append([]int(nil), field.Index...), true
	}

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}

		if field.Anonymous {
			embeddedType := field.Type
			for embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				if nestedPath, ok := findOrmFieldPathByColumnNameRecursive(embeddedType, columnName, visiting); ok {
					return append([]int{i}, nestedPath...), true
				}
			}
			continue
		}

		column, ok := ormFieldColumnName(field)
		if ok && strings.EqualFold(column, columnName) {
			return []int{i}, true
		}
	}
	return nil, false
}

func ormFieldIgnored(field reflect.StructField) bool {
	tag := field.Tag.Get("db")
	if tag == "" {
		return false
	}
	parts := strings.Split(tag, ",")
	if strings.TrimSpace(parts[0]) == "-" {
		return true
	}
	for _, option := range parts[1:] {
		if strings.TrimSpace(option) == "skip" {
			return true
		}
	}
	return false
}

func ormFieldColumnName(field reflect.StructField) (string, bool) {
	if ormFieldIgnored(field) {
		return "", false
	}
	tag := field.Tag.Get("db")
	if tag == "" {
		return "", false
	}
	column := strings.TrimSpace(strings.Split(tag, ",")[0])
	return column, column != ""
}

// ormBatchFast 直接 Scan 到结构体字段，跳过 map/any 中转。
func (o *OrmHandler) ormBatchFast(rows *sql.Rows, plan *OrmScanPlan) []any {
	var results []any
	scratch := acquireScanScratch(len(plan.columns))
	defer releaseScanScratch(scratch)

	for rows.Next() {
		instancePtr := reflect.New(plan.entityType)
		instance := instancePtr.Elem()
		o.ensureEmbeddedPaths(instance, plan.fieldPaths)

		dest, err := o.buildScanDest(instance, plan, scratch)
		if err != nil {
			LogDebug("快速 ORM 扫描构建目标失败: %v", err)
			continue
		}
		if err := rows.Scan(dest...); err != nil {
			LogDebug("快速 ORM 扫描失败: %v", err)
			continue
		}
		for i, path := range plan.fieldPaths {
			if path == nil || !plan.indirect[i] {
				continue
			}
			field, err := fieldByIndexPath(instance, path)
			if err != nil || !field.CanSet() {
				continue
			}
			val := reflect.ValueOf(*scratch.discardPtr(i))
			if !val.IsValid() {
				continue
			}
			converted, err := o.convertValue(val, field.Type())
			if err != nil {
				LogDebug("快速 ORM 间接字段转换失败: 列=%s, err=%v", plan.columns[i], err)
				continue
			}
			field.Set(converted)
		}
		results = append(results, instancePtr.Interface())
	}
	return results
}

type strictFieldScanner struct {
	orm    *OrmHandler
	field  reflect.Value
	column string
}

func (s *strictFieldScanner) Scan(source any) error {
	source = cloneStrictScanSourceIfNeeded(source, s.field.Type())
	converted, err := s.orm.convertValue(reflect.ValueOf(source), s.field.Type())
	if err != nil {
		return fmt.Errorf("列 %s 无法转换为 %s: %w", s.column, s.field.Type(), err)
	}
	s.field.Set(converted)
	return nil
}

func cloneStrictScanSourceIfNeeded(source any, targetType reflect.Type) any {
	if rawBytes, ok := source.([]byte); ok && strictScanTargetRetainsBytes(targetType) {
		return bytes.Clone(rawBytes)
	}
	return source
}

func strictScanTargetRetainsBytes(targetType reflect.Type) bool {
	for targetType.Kind() == reflect.Ptr {
		targetType = targetType.Elem()
	}
	return targetType.Kind() == reflect.Slice && targetType.Elem().Kind() == reflect.Uint8
}

// ormBatchFastStrict 使用预计算字段路径执行严格扫描。
// 自定义 Scanner 统一处理 SQL NULL，确保值字段得到零值、指针字段保持 nil。
func (o *OrmHandler) ormBatchFastStrict(rows *sql.Rows, plan *OrmScanPlan) ([]any, error) {
	if plan == nil {
		return nil, NewValidationException("严格 ORM 扫描计划不能为 nil")
	}
	if len(plan.columns) != len(plan.fieldPaths) {
		return nil, NewQueryException("严格 ORM 扫描计划列与字段路径数量不一致")
	}

	results := make([]any, 0)
	scratch := acquireScanScratch(len(plan.columns))
	defer releaseScanScratch(scratch)
	scanners := make([]strictFieldScanner, len(plan.columns))

	rowIndex := 0
	for rows.Next() {
		instancePtr := reflect.New(plan.entityType)
		instance := instancePtr.Elem()

		dest, buildErr := o.buildStrictScanDest(instance, plan, scratch, scanners)
		if buildErr != nil {
			return nil, NewQueryExceptionWithCause(buildErr, fmt.Sprintf("构建严格 ORM 扫描目标失败: row=%d", rowIndex))
		}
		if scanErr := rows.Scan(dest...); scanErr != nil {
			return nil, NewQueryExceptionWithCause(scanErr, fmt.Sprintf("严格 ORM 快速扫描失败: row=%d", rowIndex))
		}

		results = append(results, instancePtr.Interface())
		rowIndex++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, NewQueryExceptionWithCause(rowsErr, fmt.Sprintf("严格 ORM 快速行遍历失败: next_row=%d", rowIndex))
	}
	return results, nil
}

func (o *OrmHandler) buildStrictScanDest(
	instance reflect.Value,
	plan *OrmScanPlan,
	scratch *scanScratch,
	scanners []strictFieldScanner,
) ([]any, error) {
	dest := scratch.dest
	if cap(dest) < len(plan.columns) {
		dest = make([]any, len(plan.columns))
	} else {
		dest = dest[:len(plan.columns)]
	}

	for i, path := range plan.fieldPaths {
		if path == nil {
			dest[i] = scratch.discardPtr(i)
			continue
		}

		field, fieldErr := strictFieldByIndexPath(instance, path)
		if fieldErr != nil {
			return nil, fmt.Errorf("column=%s, path=%v: %w", plan.columns[i], path, fieldErr)
		}
		if !field.CanSet() {
			return nil, fmt.Errorf("column=%s, path=%v: 字段不可设置", plan.columns[i], path)
		}

		scanners[i] = strictFieldScanner{orm: o, field: field, column: plan.columns[i]}
		dest[i] = &scanners[i]
	}
	scratch.dest = dest
	return dest, nil
}

func strictFieldByIndexPath(root reflect.Value, path []int) (reflect.Value, error) {
	if len(path) == 0 {
		return reflect.Value{}, fmt.Errorf("empty field path")
	}

	current := root
	for depth, index := range path {
		if current.Kind() == reflect.Ptr {
			if current.IsNil() {
				if !current.CanSet() {
					return reflect.Value{}, fmt.Errorf("nil pointer at depth %d is not settable", depth)
				}
				current.Set(reflect.New(current.Type().Elem()))
			}
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("value at depth %d is %s, want struct", depth, current.Kind())
		}
		if index < 0 || index >= current.NumField() {
			return reflect.Value{}, fmt.Errorf("field index %d out of range at depth %d", index, depth)
		}
		current = current.Field(index)
	}
	if !current.IsValid() {
		return reflect.Value{}, fmt.Errorf("invalid field path %v", path)
	}
	return current, nil
}

func (o *OrmHandler) buildScanDest(instance reflect.Value, plan *OrmScanPlan, scratch *scanScratch) ([]any, error) {
	dest := scratch.dest
	if cap(dest) < len(plan.columns) {
		dest = make([]any, len(plan.columns))
	} else {
		dest = dest[:len(plan.columns)]
	}
	for i, path := range plan.fieldPaths {
		if path == nil {
			dest[i] = scratch.discardPtr(i)
			continue
		}
		field, err := fieldByIndexPath(instance, path)
		if err != nil || !field.CanAddr() {
			dest[i] = scratch.discardPtr(i)
			continue
		}
		if plan.indirect[i] {
			dest[i] = scratch.discardPtr(i)
		} else {
			dest[i] = field.Addr().Interface()
		}
	}
	scratch.dest = dest
	return dest, nil
}

func fieldByIndexPath(root reflect.Value, path []int) (reflect.Value, error) {
	if len(path) == 0 {
		return reflect.Value{}, fmt.Errorf("empty field path")
	}
	f := root.FieldByIndex(path)
	if !f.IsValid() {
		return f, fmt.Errorf("invalid field path %v", path)
	}
	return f, nil
}

func fieldTypeByPath(entityType reflect.Type, path []int) reflect.Type {
	for entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	if entityType.Kind() != reflect.Struct || len(path) == 0 {
		return nil
	}

	current := entityType
	for _, index := range path {
		for current.Kind() == reflect.Ptr {
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct || index < 0 || index >= current.NumField() {
			return nil
		}
		current = current.Field(index).Type
	}
	return current
}

func isDirectScannableType(t reflect.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.Bool:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.Uint8
	case reflect.Ptr:
		return isDirectScannableType(t.Elem())
	default:
		return false
	}
}

func (o *OrmHandler) ensureEmbeddedPaths(instance reflect.Value, paths [][]int) {
	for _, path := range paths {
		if len(path) <= 1 {
			continue
		}
		o.initFieldPath(instance, path[:len(path)-1])
	}
}

func (o *OrmHandler) initFieldPath(v reflect.Value, path []int) {
	if len(path) == 0 {
		return
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() && v.CanSet() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	f := v.Field(path[0])
	if len(path) == 1 {
		if f.Kind() == reflect.Ptr && f.IsNil() && f.CanSet() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		return
	}
	if f.Kind() == reflect.Ptr {
		if f.IsNil() && f.CanSet() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		o.initFieldPath(f, path[1:])
		return
	}
	o.initFieldPath(f, path[1:])
}
