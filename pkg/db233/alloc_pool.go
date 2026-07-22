package db233

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

// EnableAllocPoolEnabled 是否启用内部对象池（不影响对外返回的数据所有权）。
func EnableAllocPoolEnabled() bool {
	s := GetCrudPerformanceSettings().Snapshot()
	return s.EnableAllocPool || s.EnableRowMapPool
}

// --- field map pool（仅内部 scratch；对外仍拷贝）---

var fieldMapPool = sync.Pool{
	New: func() any {
		return make(map[string]any, 16)
	},
}

func acquireFieldMap() map[string]any {
	m := fieldMapPool.Get().(map[string]any)
	clear(m)
	return m
}

func releaseFieldMap(m map[string]any) {
	if m == nil {
		return
	}
	clear(m)
	fieldMapPool.Put(m)
}

// --- batch UPSERT / INSERT scratch ---

type batchUpsertScratch struct {
	columns      []string
	placeholders []string
	allValues    []any
	updateParts  []string
	rowValues    []any
	fieldMap     map[string]any
}

var batchUpsertScratchPool sync.Pool

const (
	maxPooledBatchScratchCapacity = 4096
	maxPooledJSONBufferCapacity   = 1 << 20
)

func acquireBatchUpsertScratch() *batchUpsertScratch {
	v := batchUpsertScratchPool.Get()
	if v == nil {
		return &batchUpsertScratch{
			fieldMap: make(map[string]any, 16),
		}
	}
	s := v.(*batchUpsertScratch)
	s.columns = s.columns[:0]
	s.placeholders = s.placeholders[:0]
	s.allValues = s.allValues[:0]
	s.updateParts = s.updateParts[:0]
	s.rowValues = s.rowValues[:0]
	clear(s.fieldMap)
	return s
}

func releaseBatchUpsertScratch(s *batchUpsertScratch) {
	if s == nil {
		return
	}
	clear(s.columns)
	clear(s.placeholders)
	clear(s.allValues)
	clear(s.updateParts)
	clear(s.rowValues)
	clear(s.fieldMap)
	if cap(s.columns) > maxPooledBatchScratchCapacity ||
		cap(s.placeholders) > maxPooledBatchScratchCapacity ||
		cap(s.allValues) > maxPooledBatchScratchCapacity ||
		cap(s.updateParts) > maxPooledBatchScratchCapacity ||
		cap(s.rowValues) > maxPooledBatchScratchCapacity {
		return
	}
	s.columns = s.columns[:0]
	s.placeholders = s.placeholders[:0]
	s.allValues = s.allValues[:0]
	s.updateParts = s.updateParts[:0]
	s.rowValues = s.rowValues[:0]
	batchUpsertScratchPool.Put(s)
}

// --- entity slice pool（WriteBuffer Flush 等内部路径）---

var entitySlicePool = sync.Pool{
	New: func() any {
		s := make([]IDbEntity, 0, 64)
		return &s
	},
}

func acquireEntitySlice(capHint int) []IDbEntity {
	p := entitySlicePool.Get().(*[]IDbEntity)
	s := *p
	s = s[:0]
	if capHint > 0 && cap(s) < capHint {
		s = make([]IDbEntity, 0, capHint)
	}
	return s
}

func releaseEntitySlice(s []IDbEntity) {
	if s == nil {
		return
	}
	clear(s)
	if cap(s) > maxPooledBatchScratchCapacity {
		return
	}
	s = s[:0]
	entitySlicePool.Put(&s)
}

// --- bytes.Buffer / strings.Builder pool ---

var byteBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

func acquireByteBuffer() *bytes.Buffer {
	b := byteBufferPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

func releaseByteBuffer(b *bytes.Buffer) {
	if b == nil {
		return
	}
	clear(b.Bytes())
	if b.Cap() > maxPooledJSONBufferCapacity {
		return
	}
	b.Reset()
	byteBufferPool.Put(b)
}

var stringBuilderPool = sync.Pool{
	New: func() any {
		return new(strings.Builder)
	},
}

func acquireStringBuilder() *strings.Builder {
	b := stringBuilderPool.Get().(*strings.Builder)
	b.Reset()
	return b
}

func releaseStringBuilder(b *strings.Builder) {
	if b == nil {
		return
	}
	b.Reset()
	stringBuilderPool.Put(b)
}

// --- IN (?,?,?) 占位符字符串缓存（不可变，只读共享）---

var inPlaceholderCache sync.Map // int -> string

const maxCachedINPlaceholder = 1024

func joinQuestionMarks(n int) string {
	if n <= 0 {
		return ""
	}
	if v, ok := inPlaceholderCache.Load(n); ok {
		return v.(string)
	}
	b := acquireStringBuilder()
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	s := b.String()
	releaseStringBuilder(b)
	if n <= maxCachedINPlaceholder {
		inPlaceholderCache.Store(n, s)
	}
	return s
}

// marshalJSONToString 使用池化 Buffer 序列化（减少 encoding 临时分配）。
func marshalJSONToString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	if str, ok := v.(string); ok {
		return str, nil
	}
	buf := acquireByteBuffer()
	enc := json.NewEncoder(buf)
	if err := enc.Encode(v); err != nil {
		releaseByteBuffer(buf)
		return "", err
	}
	raw := buf.Bytes()
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	}
	out := string(raw)
	releaseByteBuffer(buf)
	return out, nil
}
