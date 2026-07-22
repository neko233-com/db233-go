//go:build windows

package db233

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSecureAtomicExportUsesCurrentIdentityOnlyDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private", "exports")
	path := filepath.Join(dir, "metrics.json")
	if err := writeSecureAtomicFile(path, []byte("private-monitoring-data")); err != nil {
		t.Fatal(err)
	}

	wantSID := currentProcessSIDForPermissionTest(t)
	assertCurrentIdentityOnlyDACL(t, dir, wantSID)
	assertCurrentIdentityOnlyDACL(t, path, wantSID)
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
	assertCurrentIdentityOnlyDACL(t, path, currentProcessSIDForPermissionTest(t))
	assertNoSecureAtomicTemps(t, dir)
}
