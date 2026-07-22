package db233

import (
	"strings"
	"testing"
)

type entityHookPanicEntity struct {
	ID               string `json:"id" db:"id" primary_key:"true"`
	PanicSerialize   bool   `json:"panicSerialize" db:"-"`
	PanicDeserialize bool   `json:"panicDeserialize" db:"-"`
}

func (*entityHookPanicEntity) TableName() string { return "entity_hook_panic" }
func (entity *entityHookPanicEntity) SerializeBeforeSaveDb() {
	if entity.PanicSerialize {
		panic("private serialize payload")
	}
}
func (entity *entityHookPanicEntity) DeserializeAfterLoadDb() {
	if entity.PanicDeserialize {
		panic("private deserialize payload")
	}
}

func TestEntityHooksConvertPanicsToRedactedErrors(t *testing.T) {
	_, err := SerializeEntity(&entityHookPanicEntity{ID: "private-id", PanicSerialize: true})
	assertRedactedEntityHookError(t, err, "private serialize payload", "private-id")

	registry := GetEntityTypeRegistry()
	if err := registry.RegisterStrict(&entityHookPanicEntity{}); err != nil {
		t.Fatal(err)
	}
	_, err = DeserializeEntity(
		EntityTypeName(&entityHookPanicEntity{}),
		[]byte(`{"id":"private-id","panicDeserialize":true}`),
	)
	assertRedactedEntityHookError(t, err, "private deserialize payload", "private-id")
}

func assertRedactedEntityHookError(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("hook panic 未传播")
	}
	for _, value := range forbidden {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("hook panic 错误泄露业务数据 %q: %v", value, err)
		}
	}
}
