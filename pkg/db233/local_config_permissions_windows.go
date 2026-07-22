//go:build windows

package db233

import "os"

// Windows 权限由 NTFS DACL 控制，os.FileMode 无法可靠表达；本地配置仍应
// 位于服务账号私有目录，生产凭据优先从秘密管理系统注入。
func validateLocalDbConfigPermissions(_ os.FileInfo) error {
	return nil
}
