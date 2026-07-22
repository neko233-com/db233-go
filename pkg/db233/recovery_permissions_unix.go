//go:build !windows

package db233

import "os"

func hardenRecoveryDirectoryPermissions(path string) error {
	return os.Chmod(path, recoveryDirectoryMode)
}

func hardenRecoveryFilePermissions(path string) error {
	return os.Chmod(path, recoveryFileMode)
}
