package db233

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const maxLocalDbConfigFileBytes = 1 << 20

func readLocalDbConfigFile(path string) (data []byte, resultErr error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("本地配置不能是符号链接")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("关闭本地配置: %w", closeErr))
		}
	}()

	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("本地配置必须是未被替换的普通文件")
	}
	if err := validateLocalDbConfigPermissions(after); err != nil {
		return nil, err
	}

	data, err = io.ReadAll(io.LimitReader(file, maxLocalDbConfigFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLocalDbConfigFileBytes {
		return nil, fmt.Errorf("本地配置超过 %d 字节限制", maxLocalDbConfigFileBytes)
	}
	return data, nil
}
