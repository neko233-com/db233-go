package db233

import (
	"errors"
	"fmt"
	"sync"
)

// DbManager 单例类 - Go 版
// 管理多个 DbGroup 的注册、初始化、获取与销毁。
// 主要职责：
// - 保存 groupName -> DbGroup 的映射
// - 提供添加、删除、查询 DbGroup 的接口
// - 提供用户自定义的初始化入口（InitByYourDiy）
// 该类为项目中的全局入口，用于在应用启动阶段汇总并初始化所有数据源分组。
type DbManager struct {
	groupNameToDbGroupMap map[string]*DbGroup
	mu                    sync.RWMutex
	lifecycleMu           sync.Mutex
}

var instance *DbManager
var once sync.Once

// 获取单例实例（懒加载已通过 sync.Once 实现）。
// 返回: DbManager 单例实例
func GetInstance() *DbManager {
	once.Do(func() {
		instance = &DbManager{
			groupNameToDbGroupMap: make(map[string]*DbGroup),
		}
	})
	return instance
}

// 提供一个回调，让调用方以自定义方式初始化 DbManager
// fn: 一个接收 DbManager 的回调函数，调用方可以在其中调用 AddDbGroup 等方法完成自定义初始化
// 返回: error 初始化错误
func (dm *DbManager) InitByYourDiy(fn func(*DbManager) error) error {
	if fn == nil {
		return fmt.Errorf("初始化回调不能为空")
	}
	if err := fn(dm); err != nil {
		return NewDb233ExceptionWithCause(err, "自定义 DbManager 初始化失败")
	}
	return nil
}

// 获取内部的 groupName -> DbGroup 映射视图（只读视图）
// 返回: map[string]*DbGroup 包含当前已注册的所有 DbGroup
func (dm *DbManager) GetGroupNameToDbGroupMap() map[string]*DbGroup {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make(map[string]*DbGroup)
	for k, v := range dm.groupNameToDbGroupMap {
		result[k] = v
	}
	return result
}

// 根据 groupName 移除并销毁对应的 DbGroup。如果不存在则无操作。
// groupName: 要移除的分组名
func (dm *DbManager) RemoveDbGroup(groupName string) {
	if err := dm.RemoveDbGroupStrict(groupName); err != nil {
		LogError("移除 DbGroup 失败: group=%s err=%s", safeValueForLog(groupName), safeErrorForLog(err))
	}
}

// RemoveDbGroupStrict 移除并销毁分组，返回完整关闭错误链。
func (dm *DbManager) RemoveDbGroupStrict(groupName string) error {
	dm.lifecycleMu.Lock()
	defer dm.lifecycleMu.Unlock()
	dm.mu.Lock()
	dbGroup, exists := dm.groupNameToDbGroupMap[groupName]
	if exists {
		delete(dm.groupNameToDbGroupMap, groupName)
	}
	dm.mu.Unlock()
	if exists {
		return dbGroup.DestroyStrict()
	}
	return nil
}

// AddDbGroup 添加单个 DbGroup 并初始化
// 添加单个 DbGroup 并初始化
// dbGroup: 要添加的 DbGroup 对象，必须包含非空的 groupName
// 返回: error 初始化错误
func (dm *DbManager) AddDbGroup(dbGroup *DbGroup) error {
	return dm.AddDbGroups([]*DbGroup{dbGroup})
}

// AddDbGroups 添加一组 DbGroup 并逐个初始化
// 添加一组 DbGroup 并逐个初始化
// dbGroups: 要添加的 DbGroup 集合，集合中的每个 DbGroup 必须包含非空的 groupName
// 返回: error 初始化错误
func (dm *DbManager) AddDbGroups(dbGroups []*DbGroup) error {
	dm.lifecycleMu.Lock()
	defer dm.lifecycleMu.Unlock()
	names := make(map[string]struct{}, len(dbGroups))
	for _, dbGroup := range dbGroups {
		if dbGroup == nil {
			return fmt.Errorf("dbGroup 不能为空")
		}
		if dbGroup.GroupName == "" {
			return fmt.Errorf("dbGroup.GroupName 不能为空")
		}
		if _, duplicate := names[dbGroup.GroupName]; duplicate {
			return fmt.Errorf("dbGroup.GroupName 重复: %s", dbGroup.GroupName)
		}
		names[dbGroup.GroupName] = struct{}{}
	}
	dm.mu.RLock()
	for name := range names {
		if _, exists := dm.groupNameToDbGroupMap[name]; exists {
			dm.mu.RUnlock()
			return fmt.Errorf("dbGroup 已存在: %s", name)
		}
	}
	dm.mu.RUnlock()

	initialized := make([]*DbGroup, 0, len(dbGroups))
	for _, dbGroup := range dbGroups {
		if err := dbGroup.Init(); err != nil {
			rollbackErrors := []error{err}
			for index := len(initialized) - 1; index >= 0; index-- {
				if destroyErr := initialized[index].DestroyStrict(); destroyErr != nil {
					rollbackErrors = append(rollbackErrors, destroyErr)
				}
			}
			return errors.Join(rollbackErrors...)
		}
		initialized = append(initialized, dbGroup)
	}
	dm.mu.Lock()
	for _, dbGroup := range dbGroups {
		dm.groupNameToDbGroupMap[dbGroup.GroupName] = dbGroup
	}
	dm.mu.Unlock()
	return nil
}

// GetDbGroup 根据 groupName 获取对应的 DbGroup
// 根据 groupName 获取对应的 DbGroup
// groupName: 分组名
// 返回: *DbGroup 对应的 DbGroup
// 返回: error 未找到时的错误
func (dm *DbManager) GetDbGroup(groupName string) (*DbGroup, error) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	if dbGroup, exists := dm.groupNameToDbGroupMap[groupName]; exists {
		return dbGroup, nil
	}
	return nil, fmt.Errorf("没找到这个 dbGroup = %s", groupName)
}

// GetDbGroupCollection 获取当前已注册的所有 DbGroup 的集合视图
// 获取当前已注册的所有 DbGroup 的集合视图
// 返回: []*DbGroup 所有 DbGroup 的集合
func (dm *DbManager) GetDbGroupCollection() []*DbGroup {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make([]*DbGroup, 0, len(dm.groupNameToDbGroupMap))
	for _, v := range dm.groupNameToDbGroupMap {
		result = append(result, v)
	}
	return result
}
