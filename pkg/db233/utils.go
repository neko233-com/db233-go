package db233

import (
	"database/sql/driver"
	"io"
	"strings"
)

// Utils - 共享工具函数

// isConnectionError 检查是否为连接错误
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// QueryException intentionally redacts its Error string, so inspect each
	// cause without exposing it. The bound also protects against malformed
	// cyclic third-party error chains.
	pending := []error{err}
	for inspected := 0; len(pending) > 0 && inspected < 64; inspected++ {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == nil {
			continue
		}
		if current == driver.ErrBadConn || current == io.EOF {
			return true
		}
		message := strings.ToLower(safeErrorText(current))
		if strings.Contains(message, "bad connection") ||
			strings.Contains(message, "connection was forcibly closed") ||
			strings.Contains(message, "wsasend") ||
			strings.Contains(message, "broken pipe") ||
			strings.Contains(message, "connection reset") ||
			strings.Contains(message, "connection refused") ||
			strings.Contains(message, "database connection") ||
			strings.Contains(message, "数据库连接") {
			return true
		}
		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			pending = append(pending, wrapped.Unwrap()...)
		case interface{ Unwrap() error }:
			pending = append(pending, wrapped.Unwrap())
		}
	}
	return false
}
