package db233

import (
	"fmt"
	"reflect"
	"time"
)

// EntitySnapshotter 可为复杂实体提供更快或更精确的持久化深快照。
// 返回值必须与调用者后续修改完全隔离；允许保留 db:"-" 运行态字段和循环引用。
// 实现不得返回原对象、重入同一 Session/Db 写操作或执行无界阻塞。
type EntitySnapshotter interface {
	SnapshotForDb233() (IDbEntity, error)
}

const maxEntitySnapshotDepth = 1024

type entitySnapshotVisit struct {
	typ  reflect.Type
	kind reflect.Kind
	ptr  uintptr
	len  int
	cap  int
}

type entitySnapshotState struct {
	visited map[entitySnapshotVisit]reflect.Value
}

// SnapshotEntity 为 Session/异步刷写创建与业务对象完全隔离的深快照。
// 它保留指针/map 循环、相同 slice view 的别名和 slice 逻辑内容；快照
// capacity 收紧为 len，防止异常大 cap 触发无界分配。调用方必须保证快照
// 期间源对象没有并发写。实体可实现 EntitySnapshotter 避免反射开销或处理
// 含不可安全复制字段的自定义类型。
func SnapshotEntity(entity IDbEntity) (IDbEntity, error) {
	if isNilStrictValue(entity) {
		return nil, NewValidationException("实体不能为 nil")
	}
	if snapshotter, ok := entity.(EntitySnapshotter); ok {
		snapshot, err := snapshotEntityWithHook(snapshotter)
		if err != nil {
			return nil, NewValidationExceptionWithCause(err, "创建实体自定义快照失败")
		}
		if isNilStrictValue(snapshot) {
			return nil, NewValidationException("实体自定义快照不能为 nil")
		}
		if reflect.TypeOf(snapshot) != reflect.TypeOf(entity) {
			return nil, NewValidationException(fmt.Sprintf(
				"实体自定义快照类型不一致: 原类型=%T, 快照类型=%T",
				entity,
				snapshot,
			))
		}
		originalValue := reflect.ValueOf(entity)
		snapshotValue := reflect.ValueOf(snapshot)
		if originalValue.Kind() == reflect.Pointer && snapshotValue.Kind() == reflect.Pointer &&
			originalValue.Pointer() == snapshotValue.Pointer() {
			return nil, NewValidationException("实体自定义快照复用了原对象根指针")
		}
		return snapshot, nil
	}

	state := &entitySnapshotState{visited: make(map[entitySnapshotVisit]reflect.Value)}
	cloned, err := state.clone(reflect.ValueOf(entity), 0)
	if err != nil {
		return nil, NewValidationExceptionWithCause(err, fmt.Sprintf("创建实体深快照失败: Type=%T", entity))
	}
	if !cloned.IsValid() || !cloned.CanInterface() {
		return nil, NewValidationException(fmt.Sprintf("实体深快照不可用: Type=%T", entity))
	}
	snapshot, ok := cloned.Interface().(IDbEntity)
	if !ok || isNilStrictValue(snapshot) {
		return nil, NewValidationException(fmt.Sprintf("实体深快照未实现 IDbEntity: Type=%T", entity))
	}
	return snapshot, nil
}

func snapshotEntityWithHook(snapshotter EntitySnapshotter) (snapshot IDbEntity, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			snapshot = nil
			err = NewValidationException(fmt.Sprintf(
				"实体自定义快照发生 panic: %s",
				safeValueForLog(recovered),
			))
		}
	}()
	return snapshotter.SnapshotForDb233()
}

func (state *entitySnapshotState) clone(value reflect.Value, depth int) (reflect.Value, error) {
	if !value.IsValid() {
		return reflect.Value{}, nil
	}
	if depth > maxEntitySnapshotDepth {
		return reflect.Value{}, fmt.Errorf("实体引用深度超过上限 %d", maxEntitySnapshotDepth)
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		cloned, err := state.clone(value.Elem(), depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		result := reflect.New(value.Type()).Elem()
		if cloned.IsValid() {
			result.Set(cloned)
		}
		return result, nil

	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		key := entitySnapshotVisit{typ: value.Type(), kind: value.Kind(), ptr: value.Pointer()}
		if cloned, ok := state.visited[key]; ok {
			return cloned, nil
		}
		result := reflect.New(value.Type().Elem())
		state.visited[key] = result
		cloned, err := state.clone(value.Elem(), depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		result.Elem().Set(cloned)
		return result, nil

	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		key := entitySnapshotVisit{typ: value.Type(), kind: value.Kind(), ptr: uintptr(value.UnsafePointer())}
		if cloned, ok := state.visited[key]; ok {
			return cloned, nil
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		state.visited[key] = result
		iterator := value.MapRange()
		for iterator.Next() {
			clonedKey, err := state.clone(iterator.Key(), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			clonedValue, err := state.clone(iterator.Value(), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			result.SetMapIndex(clonedKey, clonedValue)
		}
		return result, nil

	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		key := entitySnapshotVisit{
			typ: value.Type(), kind: value.Kind(), ptr: value.Pointer(), len: value.Len(), cap: value.Cap(),
		}
		if key.ptr != 0 {
			if cloned, ok := state.visited[key]; ok {
				return cloned, nil
			}
		}
		// 只按逻辑 len 分配，禁止恶意/异常大 cap 导致快照 OOM。
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		if key.ptr != 0 {
			state.visited[key] = result
		}
		for index := 0; index < value.Len(); index++ {
			cloned, err := state.clone(value.Index(index), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			result.Index(index).Set(cloned)
		}
		return result, nil

	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			cloned, err := state.clone(value.Index(index), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			result.Index(index).Set(cloned)
		}
		return result, nil

	case reflect.Struct:
		if value.Type() == reflect.TypeOf(time.Time{}) {
			// time.Time 是不可变值；其内部 *time.Location 可安全共享。
			return value, nil
		}
		if unsafeEntitySnapshotRuntimeType(value.Type()) {
			return reflect.Value{}, fmt.Errorf(
				"运行态同步类型 %s 不可安全复制；请实现 EntitySnapshotter",
				value.Type(),
			)
		}
		// 从零值逐字段构造，禁止 bitwise copy 已使用的 Mutex/atomic。非导出
		// 字段保持零值；若它持有可变引用，则无法证明隔离性，要求实体显式
		// 实现 EntitySnapshotter。
		result := reflect.New(value.Type()).Elem()
		for index := 0; index < value.NumField(); index++ {
			fieldType := value.Type().Field(index)
			fieldValue := value.Field(index)
			if fieldType.Tag.Get("db233_snapshot") == "skip" {
				continue
			}
			if fieldType.PkgPath != "" {
				// 非导出且明确标记为 db:"-" 的字段是实体私有运行态，
				// 不参与后续 SQL 映射；保留零值可避免把互斥锁、缓存 map
				// 或自引用对象带入异步写入快照。
				if fieldType.Tag.Get("db") == "-" {
					continue
				}
				if unsafeEntitySnapshotRuntimeType(fieldValue.Type()) {
					continue
				}
				if !fieldValue.IsZero() {
					return reflect.Value{}, fmt.Errorf(
						"类型 %s 的非导出非零字段 %s 无法安全深拷贝；请实现 EntitySnapshotter",
						value.Type(),
						fieldType.Name,
					)
				}
				continue
			}
			if unsafeEntitySnapshotRuntimeType(fieldValue.Type()) {
				if fieldType.Tag.Get("db") == "-" {
					continue
				}
				return reflect.Value{}, fmt.Errorf(
					"导出同步字段 %s.%s 必须标记 db:\"-\" 或由 EntitySnapshotter 处理",
					value.Type(),
					fieldType.Name,
				)
			}
			if unsupportedEntitySnapshotKind(fieldValue.Kind()) {
				if fieldType.Tag.Get("db") == "-" {
					continue
				}
				return reflect.Value{}, fmt.Errorf(
					"字段 %s.%s 的类型 %s 不可安全深拷贝；请标记 db:\"-\" 或实现 EntitySnapshotter",
					value.Type(),
					fieldType.Name,
					fieldValue.Type(),
				)
			}
			cloned, err := state.clone(fieldValue, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			result.Field(index).Set(cloned)
		}
		return result, nil

	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return reflect.Value{}, fmt.Errorf("类型 %s 不可安全深拷贝；请实现 EntitySnapshotter", value.Type())

	default:
		return value, nil
	}
}

func unsafeEntitySnapshotRuntimeType(valueType reflect.Type) bool {
	return valueType.PkgPath() == "sync" || valueType.PkgPath() == "sync/atomic"
}

func unsupportedEntitySnapshotKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	default:
		return false
	}
}
