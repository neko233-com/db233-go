//go:build windows

package db233

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSecureAtomicExportUsesCurrentIdentityOnlyDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private", "exports")
	path := filepath.Join(dir, "metrics.json")
	if err := writeSecureAtomicFile(path, []byte("private-monitoring-data")); err != nil {
		t.Fatal(err)
	}

	wantSID := currentProcessSIDForSecureExport(t)
	assertSecureExportCurrentIdentityOnlyDACL(t, dir, wantSID)
	assertSecureExportCurrentIdentityOnlyDACL(t, path, wantSID)
}

func TestSecureAtomicExportReplacesExistingBroadDACLWithPrivateDACL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing-metrics.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	if err := writeSecureAtomicFile(path, []byte("new-private-content")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new-private-content" {
		t.Fatalf("覆盖内容=%q", content)
	}
	assertSecureExportCurrentIdentityOnlyDACL(t, path, currentProcessSIDForSecureExport(t))
	assertNoSecureAtomicTemps(t, dir)
}

func currentProcessSIDForSecureExport(t *testing.T) string {
	t.Helper()
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
	return user.User.Sid.String()
}

func assertSecureExportCurrentIdentityOnlyDACL(t *testing.T, path, wantSID string) {
	t.Helper()
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
		t.Fatalf("安全导出 DACL 非当前身份专用: %s", sddl)
	}
	for _, broad := range []string{"S-1-1-0", "S-1-5-11", "S-1-5-32-545", ";WD)", ";AU)", ";BU)"} {
		if strings.Contains(sddl, broad) {
			t.Fatalf("安全导出 DACL 含宽泛主体 %s: %s", broad, sddl)
		}
	}
}
