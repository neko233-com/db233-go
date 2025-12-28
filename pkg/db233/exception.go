package db233

/**
 * Db233Exception - Go 版异常类
 *
 * 对应 Kotlin/Java 版本的 Db233Exception
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
type Db233Exception struct {
	Message string
	Cause   error
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
	}
}

/**
 * 实现 error 接口
 *
 * @return string
 */
func (e *Db233Exception) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}
