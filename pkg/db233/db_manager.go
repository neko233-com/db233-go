package db233

import (
	"fmt"
	"sync"
)

/**
 * DbManager 单例类 - Go 版
 *
 * 管理多个 DbGroup 的注册、初始化、获取与销毁。
 *
 * 主要职责：
 * - 保存 groupName -> DbGroup 的映射
 * - 提供添加、删除、查询 DbGroup 的接口
 * - 提供用户自定义的初始化入口（InitByYourDiy）
 *
 * 该类为项目中的全局入口，用于在应用启动阶段汇总并初始化所有数据源分组。
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type DbManager struct {
	groupNameToDbGroupMap map[string]*DbGroup
	mu                    sync.RWMutex
}

var instance *DbManager
var once sync.Once

/**
 * 获取单例实例（懒加载已通过 sync.Once 实现）。
 *
 * @return DbManager 单例实例
 */
func GetInstance() *DbManager {
	once.Do(func() {
		instance = &DbManager{
			groupNameToDbGroupMap: make(map[string]*DbGroup),
		}
	})
	return instance
}

/**
 * 提供一个回调，让调用方以自定义方式初始化 DbManager
 *
 * @param fn 一个接收 DbManager 的回调函数，调用方可以在其中调用 AddDbGroup 等方法完成自定义初始化
 * @return error 初始化错误
 */
func (dm *DbManager) InitByYourDiy(fn func(*DbManager) error) error {
	return fn(dm)
}

/**
 * 获取内部的 groupName -> DbGroup 映射视图（只读视图）
 *
 * @return map[string]*DbGroup 包含当前已注册的所有 DbGroup
 */
func (dm *DbManager) GetGroupNameToDbGroupMap() map[string]*DbGroup {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make(map[string]*DbGroup)
	for k, v := range dm.groupNameToDbGroupMap {
		result[k] = v
	}
	return result
}

/**
 * 根据 groupName 移除并销毁对应的 DbGroup。如果不存在则无操作。
 *
 * @param groupName 要移除的分组名
 */
func (dm *DbManager) RemoveDbGroup(groupName string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dbGroup, exists := dm.groupNameToDbGroupMap[groupName]; exists {
		delete(dm.groupNameToDbGroupMap, groupName)
		dbGroup.Destroy()
	}
}

// AddDbGroup 添加单个 DbGroup 并初始化
/**
 * 添加单个 DbGroup 并初始化
 *
 * @param dbGroup 要添加的 DbGroup 对象，必须包含非空的 groupName
 * @return error 初始化错误
 */
func (dm *DbManager) AddDbGroup(dbGroup *DbGroup) error {
	return dm.AddDbGroups([]*DbGroup{dbGroup})
}

// AddDbGroups 添加一组 DbGroup 并逐个初始化
/**
 * 添加一组 DbGroup 并逐个初始化
 *
 * @param dbGroups 要添加的 DbGroup 集合，集合中的每个 DbGroup 必须包含非空的 groupName
 * @return error 初始化错误
 */
func (dm *DbManager) AddDbGroups(dbGroups []*DbGroup) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	for _, dbGroup := range dbGroups {
		if dbGroup.GroupName == "" {
			return fmt.Errorf("dbGroup.GroupName 不能为空")
		}
		dm.groupNameToDbGroupMap[dbGroup.GroupName] = dbGroup
		if err := dbGroup.Init(); err != nil {
			return err
		}
	}
	return nil
}

// GetDbGroup 根据 groupName 获取对应的 DbGroup
/**
 * 根据 groupName 获取对应的 DbGroup
 *
 * @param groupName 分组名
 * @return *DbGroup 对应的 DbGroup
 * @return error 未找到时的错误
 */
func (dm *DbManager) GetDbGroup(groupName string) (*DbGroup, error) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	if dbGroup, exists := dm.groupNameToDbGroupMap[groupName]; exists {
		return dbGroup, nil
	}
	return nil, fmt.Errorf("没找到这个 dbGroup = %s", groupName)
}

// GetDbGroupCollection 获取当前已注册的所有 DbGroup 的集合视图
/**
 * 获取当前已注册的所有 DbGroup 的集合视图
 *
 * @return []*DbGroup 所有 DbGroup 的集合
 */
func (dm *DbManager) GetDbGroupCollection() []*DbGroup {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make([]*DbGroup, 0, len(dm.groupNameToDbGroupMap))
	for _, v := range dm.groupNameToDbGroupMap {
		result = append(result, v)
	}
	return result
}
