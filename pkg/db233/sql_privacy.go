package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// runtimeLogFullSQL controls the package-level SQL logging policy. It is false
// by default. Parameters are never included by the SQL logging helpers.
var runtimeLogFullSQL atomic.Bool

// SetLogFullSQL enables or disables complete SQL text in db233 runtime logs.
//
// The default is false. When enabled, SQL is quoted with Go escaping so control
// characters cannot forge additional log records. SQL parameter values and
// raw driver error text are never logged. Error values can retain structured
// diagnostic metadata; use SafeErrorSummary at public trust boundaries.
func SetLogFullSQL(enabled bool) {
	runtimeLogFullSQL.Store(enabled)
}

// IsLogFullSQLEnabled reports the package-level SQL logging policy.
func IsLogFullSQLEnabled() bool {
	return runtimeLogFullSQL.Load()
}

// runtimeSQLLogValue defers redaction and formatting until the logger actually
// renders the argument. Its string-backed representation keeps the disabled
// hot path as small as possible.
type runtimeSQLLogValue string

func (value runtimeSQLLogValue) String() string {
	return sqlForLog(string(value), runtimeLogFullSQL.Load())
}

type componentSQLLogValue struct {
	sql         string
	includeFull bool
}

func (value componentSQLLogValue) String() string {
	return sqlForLog(value.sql, value.includeFull)
}

func sqlForRuntimeLog(sql string) runtimeSQLLogValue {
	return runtimeSQLLogValue(sql)
}

func sqlForComponentLog(sql string, includeFull bool) componentSQLLogValue {
	return componentSQLLogValue{sql: sql, includeFull: includeFull || runtimeLogFullSQL.Load()}
}

// sqlForError always returns a redacted summary. Full-SQL opt-in affects logs,
// never returned errors, because errors routinely cross trust boundaries.
func sqlForError(sql string) string {
	return sqlForLog(sql, false)
}

func sqlForLog(sql string, includeFull bool) string {
	if includeFull {
		// %q escapes CR/LF and other control characters, preventing log injection.
		return fmt.Sprintf("SQL: %q", sql)
	}
	// 默认仅暴露有限语句类别。未加盐 SQL hash 与长度可对
	// 内嵌 token/IP/玩家值做离线字典猜测，因此禁止输出。
	return fmt.Sprintf("SQLVerb: %s", sqlVerbForLog(sql))
}

func sqlVerbForLog(sql string) string {
	start := 0
	for start < len(sql) && isLogSQLSpace(sql[start]) {
		start++
	}
	end := start
	for end < len(sql) && isLogASCIILetter(sql[end]) {
		end++
		if end-start > 16 {
			return "UNKNOWN"
		}
	}
	if end == start {
		return "UNKNOWN"
	}
	verb := strings.ToUpper(sql[start:end])
	switch verb {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "REPLACE", "WITH", "CALL", "EXEC",
		"CREATE", "ALTER", "DROP", "TRUNCATE", "BEGIN", "COMMIT", "ROLLBACK",
		"SAVEPOINT", "RELEASE", "PRAGMA", "EXPLAIN", "SHOW", "DESCRIBE":
		return verb
	default:
		return "UNKNOWN"
	}
}

func isLogSQLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func isLogASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

// errorLogValue keeps arbitrary driver error text out of logs. Driver errors
// may repeat SQL literals or parameter values (for example duplicate keys).
type errorLogValue struct {
	err error
}

func (value errorLogValue) String() string {
	return safeErrorSummary(value.err)
}

func safeErrorForLog(err error) errorLogValue {
	return errorLogValue{err: err}
}

func safeErrorSummary(err error) string {
	if err == nil {
		return "ErrorType=<nil>"
	}
	return fmt.Sprintf("ErrorType=%T, ErrorClass=%s", err, safeErrorClass(err))
}

// SafeErrorSummary 返回可安全用于日志/HTTP 的稳定摘要。
// 输出只含动态类型与有限错误分类；绝不包含原始文本、
// 文本 hash 或长度，避免字典猜测与长度侧信道。
func SafeErrorSummary(err error) string {
	return safeErrorSummary(err)
}

func safeErrorClass(err error) (class string) {
	class = "other"
	defer func() {
		if recover() != nil {
			class = "other"
		}
	}()
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, sql.ErrNoRows):
		return "not_found"
	case errors.Is(err, driver.ErrBadConn), isConnectionError(err):
		return "connection"
	}
	message := safeErrorText(err)
	if strings.Contains(message, "Duplicate entry") {
		return "duplicate_key"
	}
	var validation *ValidationException
	if errors.As(err, &validation) {
		return "validation"
	}
	var transaction *TransactionException
	if errors.As(err, &transaction) {
		return "transaction"
	}
	var queryPointer *QueryException
	var queryValue QueryException
	if errors.As(err, &queryPointer) || errors.As(err, &queryValue) {
		return "query"
	}
	return class
}

func safeErrorText(err error) (message string) {
	defer func() {
		if recover() != nil {
			message = "<error message unavailable>"
		}
	}()
	return err.Error()
}

type opaqueLogValue struct {
	value any
}

func (value opaqueLogValue) String() string {
	return fmt.Sprintf("ValueType=%T", value.value)
}

func safeValueForLog(value any) opaqueLogValue {
	return opaqueLogValue{value: value}
}

// SafeValueSummary 只返回任意值的动态类型，不暴露原值、
// 原值 hash 或长度。
func SafeValueSummary(value any) string {
	return safeValueForLog(value).String()
}
