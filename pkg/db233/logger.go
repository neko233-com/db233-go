package db233

import (
	"fmt"
	"log"
	"os"
)

/**
 * Logger - 日志记录器
 *
 * 提供统一的日志记录功能，支持不同级别的日志输出
 *
 * @author SolarisNeko
 * @since 2025-12-29
 */
type Logger struct {
	level  LogLevel
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
	defaultLogger = &Logger{
		level:  INFO,
		logger: log.New(os.Stdout, "[DB233] ", log.LstdFlags),
	}
	logLevelNames = map[LogLevel]string{
		TRACE: "TRACE",
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
		FATAL: "FATAL",
	}
)

/**
 * 获取默认日志记录器
 */
func GetLogger() *Logger {
	return defaultLogger
}

/**
 * 设置日志级别
 */
func (l *Logger) SetLevel(level LogLevel) {
	l.level = level
}

/**
 * 设置输出目标
 */
func (l *Logger) SetOutput(w *os.File) {
	l.logger.SetOutput(w)
}

/**
 * 记录 TRACE 级别日志
 */
func (l *Logger) Trace(format string, args ...interface{}) {
	l.log(TRACE, format, args...)
}

/**
 * 记录 DEBUG 级别日志
 */
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(DEBUG, format, args...)
}

/**
 * 记录 INFO 级别日志
 */
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(INFO, format, args...)
}

/**
 * 记录 WARN 级别日志
 */
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(WARN, format, args...)
}

/**
 * 记录 ERROR 级别日志
 */
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
}

/**
 * 记录 FATAL 级别日志
 */
func (l *Logger) Fatal(format string, args ...interface{}) {
	l.log(FATAL, format, args...)
	os.Exit(1)
}

/**
 * 内部日志记录方法
 */
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	levelName := logLevelNames[level]
	message := fmt.Sprintf(format, args...)
	l.logger.Printf("[%s] %s", levelName, message)
}

/**
 * 便捷方法：记录 TRACE 级别日志到默认记录器
 */
func LogTrace(format string, args ...interface{}) {
	defaultLogger.Trace(format, args...)
}

/**
 * 便捷方法：记录 DEBUG 级别日志到默认记录器
 */
func LogDebug(format string, args ...interface{}) {
	defaultLogger.Debug(format, args...)
}

/**
 * 便捷方法：记录 INFO 级别日志到默认记录器
 */
func LogInfo(format string, args ...interface{}) {
	defaultLogger.Info(format, args...)
}

/**
 * 便捷方法：记录 WARN 级别日志到默认记录器
 */
func LogWarn(format string, args ...interface{}) {
	defaultLogger.Warn(format, args...)
}

/**
 * 便捷方法：记录 ERROR 级别日志到默认记录器
 */
func LogError(format string, args ...interface{}) {
	defaultLogger.Error(format, args...)
}

/**
 * 便捷方法：记录 FATAL 级别日志到默认记录器
 */
func LogFatal(format string, args ...interface{}) {
	defaultLogger.Fatal(format, args...)
}
