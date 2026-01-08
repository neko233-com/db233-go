package db233

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

/**
 * MigrationTask - 迁移任务
 */
type MigrationTask struct {
	EntityType    reflect.Type
	TableName     string
	OperationType EnumAutoDbOperateType
	SQL           string
	Priority      int // 优先级（数字越小越优先）
}

/**
 * MigrationResult - 迁移结果
 */
type MigrationResult struct {
	Task      *MigrationTask
	Success   bool
	Error     error
	Duration  time.Duration
	Timestamp time.Time
}

/**
 * ConcurrentMigrationManager - 并发迁移管理器
 *
 * 支持多协程并发执行数据库迁移任务，提高 I/O 操作效率
 *
 * @author neko233-com
 * @since 2026-01-08
 */
type ConcurrentMigrationManager struct {
	db          *Db
	permissions *AutoDbPermissions

	// 任务队列
	taskQueue chan *MigrationTask

	// 结果收集
	results      []*MigrationResult
	resultsMutex sync.Mutex

	// 统计信息
	totalTasks     int32
	completedTasks int32
	successTasks   int32
	failedTasks    int32

	// 控制
	wg       sync.WaitGroup
	ctx      chan struct{} // 用于取消
	stopOnce sync.Once
}

/**
 * NewConcurrentMigrationManager 创建并发迁移管理器
 */
func NewConcurrentMigrationManager(db *Db, permissions *AutoDbPermissions) *ConcurrentMigrationManager {
	if permissions == nil {
		permissions = NewDefaultAutoDbPermissions()
	}

	return &ConcurrentMigrationManager{
		db:          db,
		permissions: permissions,
		taskQueue:   make(chan *MigrationTask, 1000), // 缓冲1000个任务
		results:     make([]*MigrationResult, 0),
		ctx:         make(chan struct{}),
	}
}

/**
 * Start 启动并发迁移
 */
func (m *ConcurrentMigrationManager) Start() {
	if !m.permissions.EnableConcurrentMigration {
		LogWarn("并发迁移未启用，将使用单线程模式")
		m.permissions.MaxConcurrentWorkers = 1
	}

	workerCount := m.permissions.MaxConcurrentWorkers
	if workerCount <= 0 {
		workerCount = 10 // 默认10个
	}

	LogInfo("启动并发迁移管理器: 工作协程数=%d", workerCount)

	// 启动工作协程
	for i := 0; i < workerCount; i++ {
		m.wg.Add(1)
		go m.worker(i)
	}
}

/**
 * worker 工作协程
 */
func (m *ConcurrentMigrationManager) worker(id int) {
	defer m.wg.Done()

	LogDebug("迁移工作协程 #%d 已启动", id)

	for {
		select {
		case <-m.ctx:
			LogDebug("迁移工作协程 #%d 收到停止信号", id)
			return

		case task, ok := <-m.taskQueue:
			if !ok {
				LogDebug("迁移工作协程 #%d 任务队列已关闭", id)
				return
			}

			// 执行任务
			result := m.executeTask(task)

			// 收集结果
			m.resultsMutex.Lock()
			m.results = append(m.results, result)
			m.resultsMutex.Unlock()

			// 更新统计
			atomic.AddInt32(&m.completedTasks, 1)
			if result.Success {
				atomic.AddInt32(&m.successTasks, 1)
			} else {
				atomic.AddInt32(&m.failedTasks, 1)
			}

			LogDebug("迁移工作协程 #%d 完成任务: 表=%s, 操作=%s, 成功=%v, 耗时=%v",
				id, task.TableName, task.OperationType, result.Success, result.Duration)
		}
	}
}

/**
 * executeTask 执行单个迁移任务
 */
func (m *ConcurrentMigrationManager) executeTask(task *MigrationTask) *MigrationResult {
	startTime := time.Now()
	result := &MigrationResult{
		Task:      task,
		Timestamp: startTime,
	}

	// 检查权限
	if !m.permissions.IsAllowed(task.OperationType) {
		result.Success = false
		result.Error = fmt.Errorf("操作类型 %s 未被允许", task.OperationType)
		result.Duration = time.Since(startTime)
		LogWarn("迁移任务被拒绝: 表=%s, 操作=%s, 原因=权限不足", task.TableName, task.OperationType)
		return result
	}

	// Dry Run 模式
	if m.permissions.DryRun {
		LogInfo("[DRY RUN] 表=%s, 操作=%s, SQL=%s", task.TableName, task.OperationType, task.SQL)
		result.Success = true
		result.Duration = time.Since(startTime)
		return result
	}

	// 执行 SQL
	_, err := m.db.DataSource.Exec(task.SQL)
	result.Duration = time.Since(startTime)

	if err != nil {
		result.Success = false
		result.Error = err
		LogError("迁移任务执行失败: 表=%s, 操作=%s, SQL=%s, 错误=%v",
			task.TableName, task.OperationType, task.SQL, err)
	} else {
		result.Success = true
		LogInfo("迁移任务执行成功: 表=%s, 操作=%s, 耗时=%v",
			task.TableName, task.OperationType, result.Duration)
	}

	return result
}

/**
 * SubmitTask 提交迁移任务
 */
func (m *ConcurrentMigrationManager) SubmitTask(task *MigrationTask) error {
	select {
	case <-m.ctx:
		return fmt.Errorf("迁移管理器已停止")
	case m.taskQueue <- task:
		atomic.AddInt32(&m.totalTasks, 1)
		return nil
	}
}

/**
 * SubmitTasks 批量提交迁移任务
 */
func (m *ConcurrentMigrationManager) SubmitTasks(tasks []*MigrationTask) error {
	for _, task := range tasks {
		if err := m.SubmitTask(task); err != nil {
			return err
		}
	}
	return nil
}

/**
 * Stop 停止并发迁移
 */
func (m *ConcurrentMigrationManager) Stop() {
	m.stopOnce.Do(func() {
		LogInfo("停止并发迁移管理器...")
		close(m.taskQueue) // 关闭任务队列
		m.wg.Wait()        // 等待所有工作协程完成
		close(m.ctx)       // 发送停止信号
		LogInfo("并发迁移管理器已停止")
	})
}

/**
 * Wait 等待所有任务完成
 */
func (m *ConcurrentMigrationManager) Wait() {
	close(m.taskQueue) // 关闭任务队列，不再接收新任务
	m.wg.Wait()        // 等待所有工作协程完成
}

/**
 * GetResults 获取迁移结果
 */
func (m *ConcurrentMigrationManager) GetResults() []*MigrationResult {
	m.resultsMutex.Lock()
	defer m.resultsMutex.Unlock()

	// 返回副本
	results := make([]*MigrationResult, len(m.results))
	copy(results, m.results)
	return results
}

/**
 * GetStatistics 获取统计信息
 */
func (m *ConcurrentMigrationManager) GetStatistics() map[string]interface{} {
	return map[string]interface{}{
		"totalTasks":     atomic.LoadInt32(&m.totalTasks),
		"completedTasks": atomic.LoadInt32(&m.completedTasks),
		"successTasks":   atomic.LoadInt32(&m.successTasks),
		"failedTasks":    atomic.LoadInt32(&m.failedTasks),
		"pendingTasks":   len(m.taskQueue),
	}
}

/**
 * PrintStatistics 打印统计信息
 */
func (m *ConcurrentMigrationManager) PrintStatistics() {
	stats := m.GetStatistics()
	LogInfo("迁移统计: 总任务=%d, 已完成=%d, 成功=%d, 失败=%d, 待处理=%d",
		stats["totalTasks"], stats["completedTasks"], stats["successTasks"],
		stats["failedTasks"], stats["pendingTasks"])
}
