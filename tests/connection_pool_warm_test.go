package tests

import (
	"testing"

	"github.com/neko233-com/db233-go/pkg/db233"
)

func TestWarmConnectionPool_NilSafe(t *testing.T) {
	if err := db233.WarmConnectionPool(nil, 3); err != nil {
		t.Errorf("nil db 应安全返回: %v", err)
	}
}
