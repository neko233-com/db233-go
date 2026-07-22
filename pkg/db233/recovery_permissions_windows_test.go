//go:build windows

package db233

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
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
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := token.Close(); err != nil {
			t.Errorf("关闭当前进程 token: %v", err)
		}
	})
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	wantSID := user.User.Sid.String()
	for _, path := range []string{dir, file} {
		descriptor, err := windows.GetNamedSecurityInfo(
			path,
			windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION,
		)
		if err != nil {
			t.Fatal(err)
		}
		sddl := descriptor.String()
		if !strings.Contains(sddl, wantSID) || strings.Count(sddl, "(A;") != 1 {
			t.Fatalf("recovery DACL is not current-identity-only: path=%s sddl=%s", path, sddl)
		}
		for _, broad := range []string{"S-1-1-0", "S-1-5-11", "S-1-5-32-545", ";WD)", ";AU)", ";BU)"} {
			if strings.Contains(sddl, broad) {
				t.Fatalf("recovery DACL contains broad principal %s: %s", broad, sddl)
			}
		}
	}
}
