package db233

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
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
	cache map[string]*OrmScanPlan
}

var (
	ormScanPlanCacheInstance *OrmScanPlanCache
	ormScanPlanCacheOnce     sync.Once
)

// GetOrmScanPlanCache 获取扫描计划缓存单例。
func GetOrmScanPlanCache() *OrmScanPlanCache {
	ormScanPlanCacheOnce.Do(func() {
		ormScanPlanCacheInstance = &OrmScanPlanCache{
			cache: make(map[string]*OrmScanPlan),
		}
	})
	return ormScanPlanCacheInstance
}

// GetPlan 获取或构建扫描计划。
func (c *OrmScanPlanCache) GetPlan(entityPrototype any, columns []string) (*OrmScanPlan, error) {
	if entityPrototype == nil || len(columns) == 0 {
		return nil, fmt.Errorf("invalid scan plan input")
	}
	entityType := reflect.TypeOf(entityPrototype)
	if entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	key := entityType.Name() + "|" + strings.Join(columns, ",")

	c.mu.RLock()
	if plan, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return plan, nil
	}
	c.mu.RUnlock()

	metadata, err := GetEntityMetadataCacheInstance().GetOrBuild(entityPrototype)
	if err != nil {
		return nil, err
	}

	plan := &OrmScanPlan{
		entityType: entityType,
		columns:    append([]string(nil), columns...),
		fieldPaths: make([][]int, len(columns)),
		indirect:   make([]bool, len(columns)),
	}
	for i, col := range columns {
		path, ok := metadata.ColumnToFieldPath[col]
		if !ok {
			for k, p := range metadata.ColumnToFieldPath {
				if strings.EqualFold(k, col) {
					path = p
					ok = true
					break
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
	if entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	f, err := fieldByIndexPath(reflect.New(entityType).Elem(), path)
	if err != nil {
		return nil
	}
	return f.Type()
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
	case reflect.Struct:
		return t == reflect.TypeOf(time.Time{})
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
