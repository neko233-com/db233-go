package db233

import (
	"fmt"
	"sync"
)

// PlayerSession 玩家 Session L1 缓存：登录后内存驻留，读走内存，写走 dirty + 写缓冲。
type PlayerSession struct {
	PlayerID string
	repo     *BaseCrudRepository

	mu       sync.RWMutex
	entities map[string]IDbEntity // tableName -> entity
	dirty    map[string]IDbEntity // tableName -> 待落库实体
	loaded   bool
}

func newPlayerSession(playerID string, repo *BaseCrudRepository) *PlayerSession {
	return &PlayerSession{
		PlayerID: playerID,
		repo:     repo,
		entities: make(map[string]IDbEntity),
		dirty:    make(map[string]IDbEntity),
	}
}

// Load 并发加载玩家全量数据到 L1（登录时调用）。
func (s *PlayerSession) Load(entityTypes []IDbEntity) error {
	if s.PlayerID == "" {
		return NewValidationException("PlayerID 不能为空")
	}
	results := s.repo.FindByIdConcurrent(s.PlayerID, entityTypes, nil)
	var firstErr error
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range results {
		if item.EntityType == nil {
			continue
		}
		tableName := s.repo.getTableName(item.EntityType)
		if item.Err != nil && firstErr == nil {
			firstErr = item.Err
			continue
		}
		if item.Entity != nil {
			s.entities[tableName] = item.Entity
		}
	}
	s.loaded = true
	return firstErr
}

// Get 从 L1 读取实体（返回 nil 表示该表无数据，游戏服可新建）。
func (s *PlayerSession) Get(prototype IDbEntity) IDbEntity {
	if prototype == nil {
		return nil
	}
	tableName := s.repo.getTableName(prototype)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entities[tableName]
}

// Put 更新 L1 并标记 dirty，同时异步入写缓冲（高频写）。
func (s *PlayerSession) Put(entity IDbEntity) error {
	if entity == nil {
		return NewValidationException("实体不能为 nil")
	}
	tableName := s.repo.getTableName(entity)
	s.mu.Lock()
	s.entities[tableName] = entity
	s.dirty[tableName] = entity
	s.mu.Unlock()

	return s.repo.SaveBuffered(entity)
}

// MarkDirty 标记已有 L1 实体为 dirty（原地修改后调用）。
func (s *PlayerSession) MarkDirty(entity IDbEntity) error {
	if entity == nil {
		return NewValidationException("实体不能为 nil")
	}
	tableName := s.repo.getTableName(entity)
	s.mu.Lock()
	s.entities[tableName] = entity
	s.dirty[tableName] = entity
	s.mu.Unlock()
	return s.repo.SaveBuffered(entity)
}

// Flush 同步落库：dirty 批量 UPSERT（WAL 保护）+ 刷写缓冲。
func (s *PlayerSession) Flush() error {
	s.mu.Lock()
	dirtyEntities := make([]IDbEntity, 0, len(s.dirty))
	for _, entity := range s.dirty {
		dirtyEntities = append(dirtyEntities, entity)
	}
	s.dirty = make(map[string]IDbEntity)
	s.mu.Unlock()

	var firstErr error
	if len(dirtyEntities) > 0 {
		if err := s.repo.UpdateBatchUpsert(dirtyEntities); err != nil && firstErr == nil {
			firstErr = err
			// 失败时恢复 dirty 标记
			s.mu.Lock()
			for _, entity := range dirtyEntities {
				s.dirty[s.repo.getTableName(entity)] = entity
			}
			s.mu.Unlock()
		}
	}
	if err := s.repo.FlushWriteBuffer(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// IsLoaded 是否已完成登录加载。
func (s *PlayerSession) IsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// DirtyCount 待落库实体数量。
func (s *PlayerSession) DirtyCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.dirty)
}

// SessionRepository 管理在线玩家 Session（游戏逻辑服入口）。
type SessionRepository struct {
	repo     *BaseCrudRepository
	sessions sync.Map // playerID -> *PlayerSession
}

// NewSessionRepository 创建 Session 仓储。
func NewSessionRepository(repo *BaseCrudRepository) *SessionRepository {
	return &SessionRepository{repo: repo}
}

// OpenSession 登录：创建 Session 并加载全量玩家数据。
func (sr *SessionRepository) OpenSession(playerID string, entityTypes []IDbEntity) (*PlayerSession, error) {
	if playerID == "" {
		return nil, NewValidationException("PlayerID 不能为空")
	}
	if existing, ok := sr.sessions.Load(playerID); ok {
		return existing.(*PlayerSession), nil
	}
	session := newPlayerSession(playerID, sr.repo)
	if err := session.Load(entityTypes); err != nil {
		return nil, err
	}
	sr.sessions.Store(playerID, session)
	LogDebug("玩家 Session 已打开: playerID=%s, 表数=%d", playerID, len(entityTypes))
	return session, nil
}

// GetSession 获取在线 Session（不存在返回 nil）。
func (sr *SessionRepository) GetSession(playerID string) *PlayerSession {
	if v, ok := sr.sessions.Load(playerID); ok {
		return v.(*PlayerSession)
	}
	return nil
}

// CloseSession 下线：Flush 落库并移除 Session。
func (sr *SessionRepository) CloseSession(playerID string) error {
	v, ok := sr.sessions.LoadAndDelete(playerID)
	if !ok {
		return nil
	}
	session := v.(*PlayerSession)
	if err := session.Flush(); err != nil {
		// 数据已在 WAL，重新放回 session 以便排查
		sr.sessions.Store(playerID, session)
		return fmt.Errorf("玩家下线落库失败(数据已写 WAL): playerID=%s, err=%w", playerID, err)
	}
	LogDebug("玩家 Session 已关闭: playerID=%s", playerID)
	return nil
}

// OnlineCount 在线玩家数。
func (sr *SessionRepository) OnlineCount() int {
	count := 0
	sr.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// FlushAll 全服落库（关服/定时 checkpoint 时调用）。
func (sr *SessionRepository) FlushAll() error {
	var firstErr error
	sr.sessions.Range(func(key, value any) bool {
		session := value.(*PlayerSession)
		if err := session.Flush(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("playerID=%s: %w", key.(string), err)
		}
		return true
	})
	if err := sr.repo.FlushWriteBuffer(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
