//go:build !windows

package db233

import (
	"fmt"
	"os"
)

func validateLocalDbConfigPermissions(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("本地配置权限过宽: mode=%#o，必须为 0600 或更严格", info.Mode().Perm())
	}
	return nil
}
