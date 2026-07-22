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

// Add 加入 LRU；若当前容量已缩小，返回所有需淘汰的 playerID（最久未访问优先）。
func (l *sessionLRU) Add(playerID string) (evicted []string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if el, ok := l.items[playerID]; ok {
		l.order.MoveToBack(el)
		return nil
	}

	for l.order.Len() >= l.maxSize {
		front := l.order.Front()
		if front == nil {
			break
		}
		entry := front.Value.(*sessionLRUEntry)
		evicted = append(evicted, entry.playerID)
		delete(l.items, entry.playerID)
		l.order.Remove(front)
	}

	entry := &sessionLRUEntry{playerID: playerID}
	el := l.order.PushBack(entry)
	l.items[playerID] = el
	return evicted
}

// Restore 恢复此前因淘汰失败而移出的条目。允许暂时超过容量，避免丢失在线 Session。
func (l *sessionLRU) Restore(playerID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[playerID]; ok {
		l.order.MoveToFront(el)
		return
	}
	el := l.order.PushFront(&sessionLRUEntry{playerID: playerID})
	l.items[playerID] = el
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

func (l *sessionLRU) Clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.items = make(map[string]*list.Element)
	l.order.Init()
	l.mu.Unlock()
}

// SetMaxSize 动态调整最大 Session 数，并返回缩容时移出的条目。
func (l *sessionLRU) SetMaxSize(maxSize int) (evicted []string) {
	if maxSize <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxSize = maxSize
	for l.order.Len() > l.maxSize {
		front := l.order.Front()
		if front == nil {
			break
		}
		entry := front.Value.(*sessionLRUEntry)
		evicted = append(evicted, entry.playerID)
		delete(l.items, entry.playerID)
		l.order.Remove(front)
	}
	return evicted
}
