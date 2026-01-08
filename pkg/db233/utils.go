package db233

import "strings"

/**
 * Utils - 共享工具函数
 *
 * @author neko233-com
 * @since 2026-01-08
 */

// isConnectionError 检查是否为连接错误
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	// 检查常见的连接错误关键词
	return strings.Contains(errMsg, "bad connection") ||
		strings.Contains(errMsg, "connection was forcibly closed") ||
		strings.Contains(errMsg, "wsasend") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "EOF") ||
		strings.Contains(errMsg, "connection refused")
}
