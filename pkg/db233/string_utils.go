package db233

import (
	"strings"
	"unicode"
)

// StringUtilsForDb233 - 字符串工具类
// 对应 Kotlin 版本的 StringUtilsForDb233
type StringUtilsForDb233 struct{}

// 检查字符串是否为空
// str: 字符串
// 返回: bool 是否为空
func (s *StringUtilsForDb233) IsBlank(str string) bool {
	return strings.TrimSpace(str) == ""
}

// 检查字符串是否不为空
// str: 字符串
// 返回: bool 是否不为空
func (s *StringUtilsForDb233) IsNotBlank(str string) bool {
	return !s.IsBlank(str)
}

// 驼峰转下划线
// str: 驼峰字符串
// 返回: string 下划线字符串
func (s *StringUtilsForDb233) CamelToSnake(str string) string {
	var result []rune
	for i, r := range str {
		if unicode.IsUpper(r) && i > 0 {
			result = append(result, '_')
		}
		result = append(result, unicode.ToLower(r))
	}
	return string(result)
}

// 下划线转驼峰
// str: 下划线字符串
// 返回: string 驼峰字符串
func (s *StringUtilsForDb233) SnakeToCamel(str string) string {
	parts := strings.Split(str, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			runes := []rune(parts[i])
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
	}
	return strings.Join(parts, "")
}

// 连接字符串数组
// elements: 字符串数组
// separator: 分隔符
// 返回: string 连接后的字符串
func (s *StringUtilsForDb233) Join(elements []string, separator string) string {
	return strings.Join(elements, separator)
}

// 单例实例
var StringUtilsInstance = &StringUtilsForDb233{}
