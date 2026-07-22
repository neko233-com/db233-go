package db233

import (
	"bytes"
	"testing"
)

type allocPoolPrivacyEntity struct {
	ID string `db:"id" primary_key:"true"`
}

func (*allocPoolPrivacyEntity) TableName() string       { return "alloc_pool_privacy" }
func (*allocPoolPrivacyEntity) SerializeBeforeSaveDb()  {}
func (*allocPoolPrivacyEntity) DeserializeAfterLoadDb() {}

func TestAllocPoolsClearSensitiveReferencesBeforeReuse(t *testing.T) {
	entity := &allocPoolPrivacyEntity{ID: "private-player-id"}
	entities := make([]IDbEntity, 1, 4)
	entities[0] = entity
	releaseEntitySlice(entities)
	if entities[0] != nil {
		t.Fatal("entity slice pool retained player entity")
	}

	scratch := &batchUpsertScratch{
		columns:      []string{"private-column"},
		placeholders: []string{"private-placeholder"},
		allValues:    []any{"private-value"},
		updateParts:  []string{"private-update"},
		rowValues:    []any{entity},
		fieldMap:     map[string]any{"private-key": entity},
	}
	columns := scratch.columns
	values := scratch.allValues
	rows := scratch.rowValues
	releaseBatchUpsertScratch(scratch)
	if columns[0] != "" || values[0] != nil || rows[0] != nil || len(scratch.fieldMap) != 0 {
		t.Fatal("batch scratch pool retained sensitive references")
	}
}

func TestJSONBufferPoolClearsWrittenBytes(t *testing.T) {
	buffer := bytes.NewBuffer(make([]byte, 0, 64))
	buffer.WriteString("private-json-canary")
	written := buffer.Bytes()
	releaseByteBuffer(buffer)
	for index, value := range written {
		if value != 0 {
			t.Fatalf("JSON buffer byte %d retained value %d", index, value)
		}
	}
}
