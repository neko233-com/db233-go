package db233

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

/**
 * PackageScanner - Go 包扫描器
 *
 * 对应 Kotlin 版本的 PackageScanner.java
 * 由于 Go 的运行时特性，使用类型注册的方式实现包扫描
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type PackageScanner struct {
	// 已注册的类型映射
	registeredTypes map[string]reflect.Type
	mu              sync.RWMutex
}

/**
 * TypeFilter - 类型过滤器接口
 */
type TypeFilter interface {
	Accept(t reflect.Type) bool
}

/**
 * FuncTypeFilter - 函数式类型过滤器
 */
type FuncTypeFilter func(reflect.Type) bool

func (f FuncTypeFilter) Accept(t reflect.Type) bool {
	return f(t)
}

/**
 * 创建新的包扫描器
 */
func NewPackageScanner() *PackageScanner {
	return &PackageScanner{
		registeredTypes: make(map[string]reflect.Type),
	}
}

/**
 * 注册类型
 *
 * @param t 要注册的类型
 */
func (ps *PackageScanner) RegisterType(t reflect.Type) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := ps.GetTypeKey(t)
	ps.registeredTypes[key] = t
}

/**
 * 批量注册类型
 *
 * @param types 要注册的类型列表
 */
func (ps *PackageScanner) RegisterTypes(types ...reflect.Type) {
	for _, t := range types {
		ps.RegisterType(t)
	}
}

/**
 * 扫描包中的所有类型
 *
 * @param packageName 包名
 * @return []reflect.Type 类型列表
 */
func (ps *PackageScanner) ScanTypes(packageName string) []reflect.Type {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var result []reflect.Type
	for _, t := range ps.registeredTypes {
		if ps.GetPackageName(t) == packageName {
			result = append(result, t)
		}
	}
	return result
}

/**
 * 扫描包中的类型（带过滤器）
 *
 * @param packageName 包名
 * @param filter 类型过滤器
 * @return []reflect.Type 类型列表
 */
func (ps *PackageScanner) ScanTypesWithFilter(packageName string, filter TypeFilter) []reflect.Type {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var result []reflect.Type
	for _, t := range ps.registeredTypes {
		if ps.GetPackageName(t) == packageName && filter.Accept(t) {
			result = append(result, t)
		}
	}
	return result
}

/**
 * 扫描包中的结构体类型
 *
 * @param packageName 包名
 * @return []reflect.Type 结构体类型列表
 */
func (ps *PackageScanner) ScanStructTypes(packageName string) []reflect.Type {
	return ps.ScanTypesWithFilter(packageName, FuncTypeFilter(func(t reflect.Type) bool {
		return t.Kind() == reflect.Struct
	}))
}

/**
 * 扫描包中的子类型
 *
 * @param packageName 包名
 * @param superType 父类型
 * @return []reflect.Type 子类型列表
 */
func (ps *PackageScanner) ScanSubTypes(packageName string, superType reflect.Type) []reflect.Type {
	if superType == nil {
		return []reflect.Type{}
	}

	return ps.ScanTypesWithFilter(packageName, FuncTypeFilter(func(t reflect.Type) bool {
		if t.Kind() != reflect.Struct {
			return false
		}
		// 检查是否可以赋值给父类型
		return superType.AssignableTo(t) || t.AssignableTo(superType) || t.Implements(superType)
	}))
}

/**
 * 获取所有已注册的类型
 *
 * @return []reflect.Type 类型列表
 */
func (ps *PackageScanner) GetAllRegisteredTypes() []reflect.Type {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	result := make([]reflect.Type, 0, len(ps.registeredTypes))
	for _, t := range ps.registeredTypes {
		result = append(result, t)
	}
	return result
}

/**
 * 获取类型键
 */
func (ps *PackageScanner) GetTypeKey(t reflect.Type) string {
	return fmt.Sprintf("%s.%s", ps.GetPackageName(t), t.Name())
}

/**
 * 获取包名
 */
func (ps *PackageScanner) GetPackageName(t reflect.Type) string {
	// Go reflect.Type.String() 返回格式如 "package.TypeName"
	// 对于未命名类型，返回空字符串
	fullName := t.String()
	if strings.Contains(fullName, ".") {
		parts := strings.Split(fullName, ".")
		if len(parts) > 1 {
			// 去掉最后一个部分（类型名）
			packagePath := strings.Join(parts[:len(parts)-1], ".")
			return packagePath
		}
	}
	return ""
}

/**
 * 全局包扫描器实例
 */
var PackageScannerInstance = NewPackageScanner()
