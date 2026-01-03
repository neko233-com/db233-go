package db233

import (
	"fmt"
	"strings"
)

/**
 * Db233Exception - Go 版异常类
 *
 * 对应 Kotlin/Java 版本的 Db233Exception
 *
 * @author neko233-com
 * @since 2025-12-28
 */
type Db233Exception struct {
	Message string
	Cause   error
	Code    string
}

/**
 * 创建 Db233Exception
 *
 * @param message 错误消息
 * @return *Db233Exception
 */
func NewDb233Exception(message string) *Db233Exception {
	return &Db233Exception{
		Message: message,
		Code:    "DB233_ERROR",
	}
}

/**
 * 创建带原因的 Db233Exception
 *
 * @param cause 原因错误
 * @param message 错误消息
 * @return *Db233Exception
 */
func NewDb233ExceptionWithCause(cause error, message string) *Db233Exception {
	return &Db233Exception{
		Message: message,
		Cause:   cause,
		Code:    "DB233_ERROR",
	}
}

/**
 * 创建带错误码的 Db233Exception
 *
 * @param code 错误码
 * @param message 错误消息
 * @return *Db233Exception
 */
func NewDb233ExceptionWithCode(code string, message string) *Db233Exception {
	return &Db233Exception{
		Message: message,
		Code:    code,
	}
}

/**
 * 实现 error 接口
 *
 * @return string
 */
func (e *Db233Exception) Error() string {
	if e.Cause != nil {
		// 尝试解析 MySQL 错误码，提供更友好的错误信息
		causeMsg := e.Cause.Error()
		friendlyMsg := e.formatFriendlyError(causeMsg)
		return fmt.Sprintf("[%s] %s\n原因: %s", e.Code, e.Message, friendlyMsg)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

/**
 * 格式化友好的错误信息
 */
func (e *Db233Exception) formatFriendlyError(errorMsg string) string {
	// 解析常见的 MySQL 错误，提供更友好的提示
	if strings.Contains(errorMsg, "Duplicate entry") {
		return fmt.Sprintf("数据重复冲突: %s\n提示: 尝试插入的数据与现有记录的主键或唯一键冲突，请检查数据是否已存在", errorMsg)
	}
	if strings.Contains(errorMsg, "doesn't exist") {
		return fmt.Sprintf("表或列不存在: %s\n提示: 请检查表名是否正确，或使用 AutoCreateTable 自动创建表", errorMsg)
	}
	if strings.Contains(errorMsg, "Field") && strings.Contains(errorMsg, "doesn't have a default value") {
		return fmt.Sprintf("必填字段缺失: %s\n提示: 某些必填字段未提供值，请检查实体字段是否完整", errorMsg)
	}
	if strings.Contains(errorMsg, "connection") || strings.Contains(errorMsg, "Connection") {
		return fmt.Sprintf("数据库连接错误: %s\n提示: 请检查数据库服务是否运行，连接配置是否正确", errorMsg)
	}
	if strings.Contains(errorMsg, "timeout") {
		return fmt.Sprintf("操作超时: %s\n提示: 数据库操作超时，可能是网络问题或数据库负载过高", errorMsg)
	}
	return errorMsg
}

/**
 * 获取错误码
 */
func (e *Db233Exception) GetCode() string {
	return e.Code
}

/**
 * 获取原因错误
 */
func (e *Db233Exception) GetCause() error {
	return e.Cause
}

/**
 * ConnectionException - 数据库连接异常
 */
type ConnectionException struct {
	*Db233Exception
}

/**
 * 创建连接异常
 */
func NewConnectionException(message string) *ConnectionException {
	return &ConnectionException{
		Db233Exception: NewDb233ExceptionWithCode("CONNECTION_ERROR", message),
	}
}

/**
 * 创建带原因的连接异常
 */
func NewConnectionExceptionWithCause(cause error, message string) *ConnectionException {
	exc := NewDb233ExceptionWithCause(cause, message)
	exc.Code = "CONNECTION_ERROR"
	return &ConnectionException{
		Db233Exception: exc,
	}
}

/**
 * QueryException - 查询异常
 */
type QueryException struct {
	*Db233Exception
}

/**
 * 创建查询异常
 */
func NewQueryException(message string) *QueryException {
	return &QueryException{
		Db233Exception: NewDb233ExceptionWithCode("QUERY_ERROR", message),
	}
}

/**
 * 创建带原因的查询异常
 */
func NewQueryExceptionWithCause(cause error, message string) *QueryException {
	exc := NewDb233ExceptionWithCause(cause, message)
	exc.Code = "QUERY_ERROR"
	return &QueryException{
		Db233Exception: exc,
	}
}

/**
 * TransactionException - 事务异常
 */
type TransactionException struct {
	*Db233Exception
}

/**
 * 创建事务异常
 */
func NewTransactionException(message string) *TransactionException {
	return &TransactionException{
		Db233Exception: NewDb233ExceptionWithCode("TRANSACTION_ERROR", message),
	}
}

/**
 * 创建带原因的事务异常
 */
func NewTransactionExceptionWithCause(cause error, message string) *TransactionException {
	exc := NewDb233ExceptionWithCause(cause, message)
	exc.Code = "TRANSACTION_ERROR"
	return &TransactionException{
		Db233Exception: exc,
	}
}

/**
 * ConfigurationException - 配置异常
 */
type ConfigurationException struct {
	*Db233Exception
}

/**
 * 创建配置异常
 */
func NewConfigurationException(message string) *ConfigurationException {
	return &ConfigurationException{
		Db233Exception: NewDb233ExceptionWithCode("CONFIG_ERROR", message),
	}
}

/**
 * 创建带原因的配置异常
 */
func NewConfigurationExceptionWithCause(cause error, message string) *ConfigurationException {
	exc := NewDb233ExceptionWithCause(cause, message)
	exc.Code = "CONFIG_ERROR"
	return &ConfigurationException{
		Db233Exception: exc,
	}
}

/**
 * ValidationException - 验证异常
 */
type ValidationException struct {
	*Db233Exception
}

/**
 * 创建验证异常
 */
func NewValidationException(message string) *ValidationException {
	return &ValidationException{
		Db233Exception: NewDb233ExceptionWithCode("VALIDATION_ERROR", message),
	}
}

/**
 * 创建带原因的验证异常
 */
func NewValidationExceptionWithCause(cause error, message string) *ValidationException {
	exc := NewDb233ExceptionWithCause(cause, message)
	exc.Code = "VALIDATION_ERROR"
	return &ValidationException{
		Db233Exception: exc,
	}
}
