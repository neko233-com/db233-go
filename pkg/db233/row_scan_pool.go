package db233

import (
	"sync"
)

// scanScratch 复用 Scan 目标切片与丢弃列占位，降低 Query / OrmBatch 分配。
type scanScratch struct {
	dest      []any
	discards  []any
	discardPtrs []*any
}

var scanScratchPool sync.Pool

func acquireScanScratch(columnCount int) *scanScratch {
	v := scanScratchPool.Get()
	var s *scanScratch
	if v == nil {
		s = &scanScratch{}
	} else {
		s = v.(*scanScratch)
	}
	if cap(s.dest) < columnCount {
		s.dest = make([]any, columnCount)
	} else {
		s.dest = s.dest[:columnCount]
	}
	if cap(s.discards) < columnCount {
		s.discards = make([]any, columnCount)
		s.discardPtrs = make([]*any, columnCount)
		for i := range s.discards {
			s.discardPtrs[i] = &s.discards[i]
		}
	} else {
		s.discards = s.discards[:columnCount]
		s.discardPtrs = s.discardPtrs[:columnCount]
		for i := range s.discards {
			s.discards[i] = nil
		}
	}
	return s
}

func (s *scanScratch) discardPtr(i int) *any {
	return s.discardPtrs[i]
}

func releaseScanScratch(s *scanScratch) {
	scanScratchPool.Put(s)
}

// rowMapPool 复用 map[string]any 作为 Scan 中间容器（内部路径；返回给调用方前会 copy）。
var rowMapPool = sync.Pool{
	New: func() any {
		return make(map[string]any, 32)
	},
}

func acquireRowMap(capHint int) map[string]any {
	m := rowMapPool.Get().(map[string]any)
	clear(m)
	if capHint > 0 && capHint > len(m) {
		// 预扩容由 make 在 New 中完成；大列数场景仍按需分配返回 map
	}
	return m
}

func releaseRowMap(m map[string]any) {
	if m == nil {
		return
	}
	clear(m)
	rowMapPool.Put(m)
}

// copyRowMap 从池化中间 map 拷贝为独立 map（对外 API 安全返回）。
func copyRowMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// scanRowsToMaps 扫描 rows 为 []map[string]any；中间 Scan 缓冲池化，返回独立 map。
func scanRowsToMaps(columns []string, scanFn func(dest []any) error) (map[string]any, error) {
	scratch := acquireScanScratch(len(columns))
	defer releaseScanScratch(scratch)

	dest := scratch.dest
	for i := range dest {
		dest[i] = scratch.discardPtr(i)
	}
	if err := scanFn(dest); err != nil {
		return nil, err
	}

	pooled := acquireRowMap(len(columns))
	for i, col := range columns {
		pooled[col] = *scratch.discardPtr(i)
	}
	out := copyRowMap(pooled)
	releaseRowMap(pooled)
	return out, nil
}
