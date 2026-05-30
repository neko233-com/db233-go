package db233

import (
	"container/list"
	"sync"
)

// sessionLRU 玩家 Session LRU 淘汰（默认策略）。
type sessionLRU struct {
	maxSize int
	items   map[string]*list.Element
	order   *list.List
	mu      sync.Mutex
}

type sessionLRUEntry struct {
	playerID string
}

func newSessionLRU(maxSize int) *sessionLRU {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &sessionLRU{
		maxSize: maxSize,
		items:   make(map[string]*list.Element),
		order:   list.New(),
	}
}

// Touch 访问 Session，移到 LRU 尾部。
func (l *sessionLRU) Touch(playerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[playerID]; ok {
		l.order.MoveToBack(el)
	}
}

// Add 加入 LRU；若超出容量返回需淘汰的 playerID（最久未访问）。
func (l *sessionLRU) Add(playerID string) (evicted string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if el, ok := l.items[playerID]; ok {
		l.order.MoveToBack(el)
		return ""
	}

	if l.order.Len() >= l.maxSize {
		front := l.order.Front()
		if front != nil {
			entry := front.Value.(*sessionLRUEntry)
			evicted = entry.playerID
			delete(l.items, evicted)
			l.order.Remove(front)
		}
	}

	entry := &sessionLRUEntry{playerID: playerID}
	el := l.order.PushBack(entry)
	l.items[playerID] = el
	return evicted
}

// Remove 从 LRU 移除。
func (l *sessionLRU) Remove(playerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[playerID]; ok {
		l.order.Remove(el)
		delete(l.items, playerID)
	}
}

// Len 当前 LRU 条目数。
func (l *sessionLRU) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.order.Len()
}

// SetMaxSize 动态调整最大 Session 数。
func (l *sessionLRU) SetMaxSize(maxSize int) {
	if maxSize <= 0 {
		return
	}
	l.mu.Lock()
	l.maxSize = maxSize
	l.mu.Unlock()
}
