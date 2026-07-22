//go:build windows

package db233

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryPathsUseCurrentIdentityOnlyDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recovery")
	if err := ensurePrivateRecoveryDirectory(dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "pending.ndjson")
	if err := os.WriteFile(file, []byte("secret"), recoveryFileMode); err != nil {
		t.Fatal(err)
	}
	if err := hardenRecoveryFilePermissions(file); err != nil {
		t.Fatal(err)
	}
	wantSID := currentProcessSIDForPermissionTest(t)
	for _, path := range []string{dir, file} {
		assertCurrentIdentityOnlyDACL(t, path, wantSID)
	}
}
