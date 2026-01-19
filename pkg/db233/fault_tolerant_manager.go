package db233

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FailedOperation keeps a failed write for retry.
type FailedOperation struct {
	ID         string         `json:"id"`
	Operation  string         `json:"operation"` // "Save", "Update", "Delete", "ExecuteUpdate"
	SQL        string         `json:"sql"`
	Params     []any          `json:"params"`
	EntityData map[string]any `json:"entity_data,omitempty"`
	TableName  string         `json:"table_name"`
	PrimaryKey any            `json:"primary_key,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
	RetryCount int            `json:"retry_count"`
	LastError  string         `json:"last_error,omitempty"`
}

// FaultTolerantManager provides reconnect and retry.
type FaultTolerantManager struct {
	db             *Db
	dbConfig       *DbConnectionConfig
	reconnectMutex sync.RWMutex
	isReconnecting bool
	lastReconnect  time.Time

	failedOps      []*FailedOperation
	failedOpsMutex sync.RWMutex
	persistPath    string

	maxReconnectAttempts int
	reconnectInterval    time.Duration
	healthCheckInterval  time.Duration

	maxRetryAttempts int
	retryInterval    time.Duration

	stopChan          chan bool
	healthCheckTicker *time.Ticker
	retryTicker       *time.Ticker
}

// NewFaultTolerantManager creates a manager with defaults.
func NewFaultTolerantManager(db *Db, dbConfig *DbConnectionConfig) *FaultTolerantManager {
	persistPath := filepath.Join(".", ".db233_failed_ops")
	ftm := &FaultTolerantManager{
		db:                   db,
		dbConfig:             dbConfig,
		failedOps:            make([]*FailedOperation, 0),
		persistPath:          persistPath,
		maxReconnectAttempts: 10,
		reconnectInterval:    5 * time.Second,
		healthCheckInterval:  30 * time.Second,
		maxRetryAttempts:     3,
		retryInterval:        10 * time.Second,
		stopChan:             make(chan bool),
	}

	ftm.loadFailedOperations()
	return ftm
}

// SetPersistPath sets the path for persistence.
func (ftm *FaultTolerantManager) SetPersistPath(path string) {
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()
	ftm.persistPath = path
}

// Start launches health check and retry loops.
func (ftm *FaultTolerantManager) Start() {
	LogInfo("容错管理器启动: 健康检查间隔=%v, 重试间隔=%v", ftm.healthCheckInterval, ftm.retryInterval)
	ftm.healthCheckTicker = time.NewTicker(ftm.healthCheckInterval)
	go ftm.healthCheckLoop()

	ftm.retryTicker = time.NewTicker(ftm.retryInterval)
	go ftm.retryLoop()
}

// Stop stops loops and persists failed operations.
func (ftm *FaultTolerantManager) Stop() {
	LogInfo("容错管理器停止")
	ftm.stopChan <- true

	if ftm.healthCheckTicker != nil {
		ftm.healthCheckTicker.Stop()
	}
	if ftm.retryTicker != nil {
		ftm.retryTicker.Stop()
	}

	ftm.persistFailedOperations()
}

func (ftm *FaultTolerantManager) healthCheckLoop() {
	for {
		select {
		case <-ftm.healthCheckTicker.C:
			ftm.checkAndReconnect()
		case <-ftm.stopChan:
			return
		}
	}
}

func (ftm *FaultTolerantManager) retryLoop() {
	for {
		select {
		case <-ftm.retryTicker.C:
			ftm.retryFailedOperations()
		case <-ftm.stopChan:
			return
		}
	}
}

// CheckAndReconnect triggers a health check and reconnect.
func (ftm *FaultTolerantManager) CheckAndReconnect() {
	ftm.checkAndReconnect()
}

func (ftm *FaultTolerantManager) checkAndReconnect() {
	ftm.reconnectMutex.Lock()
	defer ftm.reconnectMutex.Unlock()

	if ftm.isReconnecting {
		return
	}

	hc := NewHealthChecker(ftm.db)
	result := hc.Check()
	if !result.Healthy {
		LogWarn("数据库连接不健康，开始重连: %s", result.Message)
		ftm.reconnect()
	}
}

func (ftm *FaultTolerantManager) reconnect() {
	if ftm.isReconnecting {
		return
	}

	if time.Since(ftm.lastReconnect) < ftm.reconnectInterval {
		return
	}

	ftm.isReconnecting = true
	defer func() {
		ftm.isReconnecting = false
		ftm.lastReconnect = time.Now()
	}()

	LogInfo("开始重连数据库...")

	for attempt := 1; attempt <= ftm.maxReconnectAttempts; attempt++ {
		if ftm.db.DataSource != nil {
			_ = ftm.db.DataSource.Close()
		}

		newDataSource, err := ftm.dbConfig.CreateDataSource()
		if err != nil {
			LogWarn("重连尝试 %d/%d 失败: %v", attempt, ftm.maxReconnectAttempts, err)
			if attempt < ftm.maxReconnectAttempts {
				time.Sleep(ftm.reconnectInterval)
			}
			continue
		}

		ftm.db.DataSource = newDataSource
		LogInfo("数据库重连成功(尝试 %d/%d)", attempt, ftm.maxReconnectAttempts)
		go ftm.retryFailedOperations()
		return
	}

	LogError("数据库重连失败，已尝试 %d 次", ftm.maxReconnectAttempts)
}

// RecordFailedOperation adds a failed write.
func (ftm *FaultTolerantManager) RecordFailedOperation(op *FailedOperation) {
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()

	op.ID = fmt.Sprintf("%d_%s", time.Now().UnixNano(), op.Operation)
	op.Timestamp = time.Now()
	op.RetryCount = 0

	ftm.failedOps = append(ftm.failedOps, op)
	ftm.persistFailedOperations()

	LogWarn("记录失败操作: ID=%s, Operation=%s, Table=%s", op.ID, op.Operation, op.TableName)
}

func (ftm *FaultTolerantManager) retryFailedOperations() {
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()

	if len(ftm.failedOps) == 0 {
		return
	}

	hc := NewHealthChecker(ftm.db)
	result := hc.Check()
	if !result.Healthy {
		LogDebug("连接不健康，跳过重试: %s", result.Message)
		go ftm.reconnect()
		return
	}

	LogInfo("开始重试失败操作, 总数=%d", len(ftm.failedOps))
	remainingOps := make([]*FailedOperation, 0)

	for _, op := range ftm.failedOps {
		if op.RetryCount >= ftm.maxRetryAttempts {
			LogError("操作重试次数已达上限，放弃: ID=%s, Operation=%s", op.ID, op.Operation)
			continue
		}

		op.RetryCount++
		success := ftm.executeFailedOperation(op)
		if success {
			LogInfo("失败操作重试成功: ID=%s, Operation=%s, 重试次数=%d", op.ID, op.Operation, op.RetryCount)
		} else {
			op.LastError = fmt.Sprintf("重试失败 (第 %d 次)", op.RetryCount)
			remainingOps = append(remainingOps, op)
			LogWarn("失败操作重试失败: ID=%s, Operation=%s, 重试次数=%d", op.ID, op.Operation, op.RetryCount)
		}
	}

	ftm.failedOps = remainingOps
	ftm.persistFailedOperations()
}

func (ftm *FaultTolerantManager) executeFailedOperation(op *FailedOperation) bool {
	defer func() {
		if r := recover(); r != nil {
			LogError("执行失败操作时发生 panic: %v, Operation=%s", r, op.Operation)
		}
	}()

	switch op.Operation {
	case "Save", "Update":
		return ftm.executeSaveOrUpdate(op)
	case "Delete":
		return ftm.executeDelete(op)
	case "ExecuteUpdate":
		return ftm.executeUpdate(op)
	default:
		LogWarn("未知的操作类型: %s", op.Operation)
		return false
	}
}

func (ftm *FaultTolerantManager) executeSaveOrUpdate(op *FailedOperation) bool {
	if len(op.Params) == 0 {
		LogWarn("操作参数为空: ID=%s", op.ID)
		return false
	}

	result, err := ftm.db.DataSource.Exec(op.SQL, op.Params...)
	if err != nil {
		LogError("执行失败操作时出错: ID=%s, Error=%v", op.ID, err)
		return false
	}

	rowsAffected, _ := result.RowsAffected()
	LogDebug("失败操作执行成功: ID=%s, Operation=%s, 影响行数=%d", op.ID, op.Operation, rowsAffected)
	return true
}

func (ftm *FaultTolerantManager) executeDelete(op *FailedOperation) bool {
	if len(op.Params) == 0 {
		LogWarn("删除操作参数为空: ID=%s", op.ID)
		return false
	}

	result, err := ftm.db.DataSource.Exec(op.SQL, op.Params...)
	if err != nil {
		LogError("执行删除操作时出错: ID=%s, Error=%v", op.ID, err)
		return false
	}

	rowsAffected, _ := result.RowsAffected()
	LogDebug("删除操作执行成功: ID=%s, 影响行数=%d", op.ID, rowsAffected)
	return true
}

func (ftm *FaultTolerantManager) executeUpdate(op *FailedOperation) bool {
	return ftm.executeSaveOrUpdate(op)
}

func (ftm *FaultTolerantManager) persistFailedOperations() {
	if ftm.persistPath == "" {
		return
	}

	if err := os.MkdirAll(ftm.persistPath, 0755); err != nil {
		LogError("创建持久化目录失败: %v", err)
		return
	}

	filePath := filepath.Join(ftm.persistPath, "failed_operations.json")
	data, err := json.MarshalIndent(ftm.failedOps, "", "  ")
	if err != nil {
		LogError("序列化失败操作失败: %v", err)
		return
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		LogError("写入持久化文件失败: %v", err)
		return
	}

	LogDebug("失败操作已持久化: 文件=%s, 数量=%d", filePath, len(ftm.failedOps))
}

func (ftm *FaultTolerantManager) loadFailedOperations() {
	if ftm.persistPath == "" {
		return
	}

	filePath := filepath.Join(ftm.persistPath, "failed_operations.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			LogDebug("持久化文件不存在，跳过加载: %s", filePath)
			return
		}
		LogError("读取持久化文件失败: %v", err)
		return
	}

	var ops []*FailedOperation
	if err := json.Unmarshal(data, &ops); err != nil {
		LogError("反序列化失败操作失败: %v", err)
		return
	}

	ftm.failedOpsMutex.Lock()
	ftm.failedOps = ops
	ftm.failedOpsMutex.Unlock()

	LogInfo("加载持久化的失败操作: 数量=%d", len(ops))
}

// GetFailedOperationCount returns failed operations count.
func (ftm *FaultTolerantManager) GetFailedOperationCount() int {
	ftm.failedOpsMutex.RLock()
	defer ftm.failedOpsMutex.RUnlock()
	return len(ftm.failedOps)
}

// ClearFailedOperations clears all failed operations.
func (ftm *FaultTolerantManager) ClearFailedOperations() {
	ftm.failedOpsMutex.Lock()
	defer ftm.failedOpsMutex.Unlock()

	ftm.failedOps = make([]*FailedOperation, 0)
	ftm.persistFailedOperations()
	LogInfo("已清除所有失败操作")
}
