package db233

import (
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
)

// Logger - 日志记录器
// 提供统一的日志记录功能，支持不同级别的日志输出
type Logger struct {
	level  atomic.Int32
	logger *log.Logger
}

type LogLevel int

const (
	TRACE LogLevel = iota
	DEBUG
	INFO
	WARN
	ERROR
	FATAL
)

var (
	defaultLogger = newLogger(INFO, log.New(os.Stdout, "[DB233] ", log.LstdFlags))
	logLevelNames = [...]string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"}
)

func newLogger(level LogLevel, backend *log.Logger) *Logger {
	logger := &Logger{logger: backend}
	logger.level.Store(int32(level))
	return logger
}

// 获取默认日志记录器
func GetLogger() *Logger {
	return defaultLogger
}

// SetLevel 设置日志级别。非法等级会被忽略，保留当前配置。
func (l *Logger) SetLevel(level LogLevel) {
	if !level.valid() {
		return
	}
	l.level.Store(int32(level))
}

// GetLevel 获取当前日志级别。
func (l *Logger) GetLevel() LogLevel {
	return LogLevel(l.level.Load())
}

// 设置输出目标
func (l *Logger) SetOutput(w *os.File) {
	l.logger.SetOutput(w)
}

// 记录 TRACE 级别日志
func (l *Logger) Trace(format string, args ...any) {
	l.log(TRACE, format, args...)
}

// 记录 DEBUG 级别日志
func (l *Logger) Debug(format string, args ...any) {
	l.log(DEBUG, format, args...)
}

// 记录 INFO 级别日志
func (l *Logger) Info(format string, args ...any) {
	l.log(INFO, format, args...)
}

// 记录 WARN 级别日志
func (l *Logger) Warn(format string, args ...any) {
	l.log(WARN, format, args...)
}

// 记录 ERROR 级别日志
func (l *Logger) Error(format string, args ...any) {
	l.log(ERROR, format, args...)
}

// 记录 FATAL 级别日志
func (l *Logger) Fatal(format string, args ...any) {
	l.log(FATAL, format, args...)
	os.Exit(1)
}

// 内部日志记录方法
func (l *Logger) log(level LogLevel, format string, args ...any) {
	l.logWithPolicy(level, false, format, args...)
}

// logRuntime is used by the package-level runtime helpers. Bare strings may
// contain table names, paths, player identifiers, configuration keys, or
// integration names, so they are opaque unless a dedicated safe formatter is
// supplied (for example sqlForRuntimeLog or safeValueForLog).
func (l *Logger) logRuntime(level LogLevel, format string, args ...any) {
	l.logWithPolicy(level, true, format, args...)
}

func (l *Logger) logWithPolicy(level LogLevel, redactStrings bool, format string, args ...any) {
	if !level.valid() || level < LogLevel(l.level.Load()) {
		return
	}

	// Driver errors can echo SQL literals or bound values. Redact error
	// arguments centrally. Runtime helpers additionally redact bare strings.
	// The copy only occurs for an enabled record with a sensitive argument.
	var redacted []any
	for index, arg := range args {
		var replacement any
		if err, ok := arg.(error); ok {
			replacement = safeErrorForLog(err)
		} else if redactStrings && isRuntimeLogString(arg) {
			replacement = safeValueForLog(arg)
		} else {
			continue
		}
		if redacted == nil {
			redacted = append([]any(nil), args...)
		}
		redacted[index] = replacement
	}
	if redacted != nil {
		args = redacted
	}

	levelName := logLevelNames[int(level)]
	message := escapeLogRecord(fmt.Sprintf(format, args...))
	l.logger.Printf("[%s] %s", levelName, message)
}

func isRuntimeLogString(value any) bool {
	// This unexported wrapper is the only string-like runtime argument that
	// has already applied its own SQL logging policy.
	if _, ok := value.(runtimeSQLLogValue); ok {
		return false
	}
	valueType := reflect.TypeOf(value)
	return valueType != nil && valueType.Kind() == reflect.String
}

// escapeLogRecord keeps user-controlled names, paths, and identifiers from
// forging extra records. SQL values have stricter redaction in sql_privacy.go.
func escapeLogRecord(message string) string {
	if !strings.ContainsAny(message, "\r\n") {
		return message
	}
	message = strings.ReplaceAll(message, "\r", `\r`)
	return strings.ReplaceAll(message, "\n", `\n`)
}

func (level LogLevel) valid() bool {
	return level >= TRACE && level <= FATAL
}

// 便捷方法：记录 TRACE 级别日志到默认记录器
func LogTrace(format string, args ...any) {
	defaultLogger.logRuntime(TRACE, format, args...)
}

// 便捷方法：记录 DEBUG 级别日志到默认记录器
func LogDebug(format string, args ...any) {
	defaultLogger.logRuntime(DEBUG, format, args...)
}

// 便捷方法：记录 INFO 级别日志到默认记录器
func LogInfo(format string, args ...any) {
	defaultLogger.logRuntime(INFO, format, args...)
}

// 便捷方法：记录 WARN 级别日志到默认记录器
func LogWarn(format string, args ...any) {
	defaultLogger.logRuntime(WARN, format, args...)
}

// 便捷方法：记录 ERROR 级别日志到默认记录器
func LogError(format string, args ...any) {
	defaultLogger.logRuntime(ERROR, format, args...)
}

// 便捷方法：记录 FATAL 级别日志到默认记录器
func LogFatal(format string, args ...any) {
	defaultLogger.logRuntime(FATAL, format, args...)
	os.Exit(1)
}
