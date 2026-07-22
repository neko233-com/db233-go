//go:build !windows

package db233

import "os"

func replaceSecureAtomicFile(source, destination string) error {
	return os.Rename(source, destination)
}
