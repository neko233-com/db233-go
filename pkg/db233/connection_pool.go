package db233

import (
	"database/sql"
	"sync"
	"time"
)

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

// RegisterDbForConnectionPool 注册 Db，配置变更时自动刷新连接池。
func RegisterDbForConnectionPool(db *Db) {
	if db == nil {
		return
	}
	registeredPoolDbs.Store(db, struct{}{})
	settings := GetCrudPerformanceSettings().Snapshot()
	ApplyConnectionPoolSettings(db.DataSource, settings)
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
