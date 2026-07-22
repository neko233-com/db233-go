//go:build windows

package db233

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func currentProcessSIDForPermissionTest(t *testing.T) *windows.SID {
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
	return user.User.Sid
}

func assertCurrentIdentityOnlyDACL(t *testing.T, path string, wantSID *windows.SID) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL 未阻止继承: path=%s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("DACL ACE 数量不是 1: path=%s", path)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("DACL 唯一 ACE 不是允许规则: path=%s", path)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(wantSID) {
		t.Fatalf("DACL 唯一主体不是当前身份: path=%s", path)
	}
	if ace.Mask&windows.FILE_GENERIC_READ != windows.FILE_GENERIC_READ ||
		ace.Mask&windows.FILE_GENERIC_WRITE != windows.FILE_GENERIC_WRITE {
		t.Fatalf("DACL 当前身份缺少读写权限: path=%s", path)
	}
}
