//go:build windows

package db233

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// Windows 的 os.Chmod 不能收紧 DACL。恢复文件含 SQL 参数、实体快照与主键，
// 必须用受保护 DACL 仅授权当前进程身份（交互用户或服务账号）完全控制。
// 目录 ACE 向子目录/文件继承；单文件仍设置显式受保护 DACL，避免目录被移动或
// 历史 ACL 残留。任何 ACL 设置失败都 fail-fast。
func hardenRecoveryDirectoryPermissions(path string) error {
	return setRecoveryPathDACL(path, true)
}

func hardenRecoveryFilePermissions(path string) error {
	return setRecoveryPathDACL(path, false)
}

func setRecoveryPathDACL(path string, directory bool) (resultErr error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("打开当前进程 token: %w", err)
	}
	defer func() {
		if closeErr := token.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("关闭当前进程 token: %w", closeErr))
		}
	}()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("读取当前进程身份: %w", err)
	}
	sid := user.User.Sid.String()
	aceFlags := ""
	if directory {
		aceFlags = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;" + aceFlags + ";FA;;;" + sid + ")",
	)
	if err != nil {
		return fmt.Errorf("构建恢复路径 DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("读取恢复路径 DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("设置恢复路径 DACL: %w", err)
	}
	return nil
}
