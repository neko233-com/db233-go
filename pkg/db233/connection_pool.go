package db233

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// DefaultWarmupTimeout bounds compatibility warm-up calls that do not accept
// a caller context. Context-aware callers should use WarmConnectionPoolContext
// or WarmGameDbContext and choose a deadline appropriate for startup.
const DefaultWarmupTimeout = 30 * time.Second

var registeredPoolDbs sync.Map // *Db -> struct{}

// ApplyConnectionPoolSettings 将性能配置应用到连接池（启动时调用一次）。
func ApplyConnectionPoolSettings(db *sql.DB, settings CrudPerformanceSettings) {
	if db == nil {
		return
	}
	if settings.MaxOpenConns > 0 {
		db.SetMaxOpenConns(settings.MaxOpenConns)
	}
	if settings.MaxIdleConns > 0 {
		db.SetMaxIdleConns(settings.MaxIdleConns)
	}
	if settings.ConnMaxLifetimeSec > 0 {
		db.SetConnMaxLifetime(time.Duration(settings.ConnMaxLifetimeSec) * time.Second)
	}
	if settings.ConnMaxIdleTimeSec > 0 {
		db.SetConnMaxIdleTime(time.Duration(settings.ConnMaxIdleTimeSec) * time.Second)
	}
	LogInfo("连接池已配置: maxOpen=%d, maxIdle=%d, lifetime=%ds, idleTime=%ds",
		settings.MaxOpenConns, settings.MaxIdleConns, settings.ConnMaxLifetimeSec, settings.ConnMaxIdleTimeSec)
}

// WarmConnectionPool 预热连接池（降低首条 SQL 冷启动延迟）。
func WarmConnectionPool(db *sql.DB, rounds int) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultWarmupTimeout)
	defer cancel()
	return WarmConnectionPoolContext(ctx, db, rounds)
}

// WarmConnectionPoolContext 在调用方的 deadline/cancel 内预热连接池。
// 所有 Ping 失败都会被保留，不会只返回最后一次错误。
func WarmConnectionPoolContext(ctx context.Context, db *sql.DB, rounds int) error {
	if ctx == nil {
		return NewValidationException("连接池预热 context 不能为 nil")
	}
	if db == nil || rounds <= 0 {
		return nil
	}
	warmErrors := make([]error, 0, min(rounds, 8))
	for i := 0; i < rounds; i++ {
		if err := ctx.Err(); err != nil {
			warmErrors = append(warmErrors, err)
			break
		}
		if err := db.PingContext(ctx); err != nil {
			warmErrors = append(warmErrors, NewConnectionExceptionWithCause(err, "连接池预热失败"))
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
		}
	}
	return errors.Join(warmErrors...)
}

// RegisterDbForConnectionPool 注册 Db，配置变更时自动刷新连接池。
func RegisterDbForConnectionPool(db *Db) {
	if db == nil {
		return
	}
	db.resourceMu.Lock()
	if db.closing {
		db.resourceMu.Unlock()
		return
	}
	registeredPoolDbs.Store(db, struct{}{})
	db.resourceMu.Unlock()
	settings := GetCrudPerformanceSettings().Snapshot()
	ApplyConnectionPoolSettings(db.DataSource, settings)
}

// UnregisterDbForConnectionPool 取消热更新跟踪，释放已关闭 Db 的全局引用。
func UnregisterDbForConnectionPool(db *Db) {
	if db == nil {
		return
	}
	registeredPoolDbs.Delete(db)
}

func initConnectionPoolOnChange() {
	GetCrudPerformanceSettings().OnChange(func(settings CrudPerformanceSettings) {
		registeredPoolDbs.Range(func(key, _ any) bool {
			if db, ok := key.(*Db); ok && db.DataSource != nil {
				ApplyConnectionPoolSettings(db.DataSource, settings)
			}
			return true
		})
	})
}

func init() {
	initConnectionPoolOnChange()
}
