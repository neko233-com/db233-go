package db233

import (
	"context"
	"crypto/sha256"
	stdsql "database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultEntityMigrationLockTimeoutSeconds 是跨实例等待实体迁移锁的默认超时秒数。
	DefaultEntityMigrationLockTimeoutSeconds = 60
	// entityMigrationTableName 是 db233 管理的实体数据迁移记录表。
	entityMigrationTableName = "db233_entity_migrations"
)

var (
	// ErrEntityMigrationPending 表示 DryRun 或禁止执行时仍存在待应用的数据迁移。
	ErrEntityMigrationPending = errors.New("db233: entity data migrations pending")
	// ErrEntityMigrationDefinitionChanged 表示已应用迁移的定义被修改。
	ErrEntityMigrationDefinitionChanged = errors.New("db233: applied entity migration definition changed")
	// ErrUnknownAppliedEntityMigration 表示数据库含有当前程序未注册的迁移，通常意味着错误回滚了旧程序。
	ErrUnknownAppliedEntityMigration = errors.New("db233: unknown applied entity migration")
)

// EntityDataMigrationFunc 在受控事务内执行或校验业务数据迁移。
// 回调只能执行 DML/DQL；DDL 必须由 schema 编排阶段处理。
type EntityDataMigrationFunc func(context.Context, *EntityDataMigrationTx) error

// EntityDataMigration 描述一个不可变、版本化的业务 Entity 数据迁移。
// Scope 通常使用 TableName；Version 在同一 Scope 内严格递增；Order 控制跨 Entity 的全局执行顺序。
// Fingerprint 是业务维护的不可变迁移定义摘要，修改迁移逻辑时必须新增版本，禁止覆盖旧值。
type EntityDataMigration struct {
	Scope       string                  `json:"scope"`
	Version     int64                   `json:"version"`
	Order       int64                   `json:"order"`
	Name        string                  `json:"name"`
	Fingerprint string                  `json:"fingerprint"`
	Up          EntityDataMigrationFunc `json:"-"`
	Verify      EntityDataMigrationFunc `json:"-"`
}

// EntityDataMigrationOptions 控制实体数据迁移执行。
type EntityDataMigrationOptions struct {
	Namespace           string `json:"namespace"`
	DryRun              bool   `json:"dryRun"`
	LockTimeoutSeconds  int    `json:"lockTimeoutSeconds"`
	AllowUnknownApplied bool   `json:"allowUnknownApplied"`
}

// EntityDataMigrationRecord 是已应用迁移的审计记录。
type EntityDataMigrationRecord struct {
	Namespace string    `json:"namespace"`
	Scope     string    `json:"scope"`
	Version   int64     `json:"version"`
	Order     int64     `json:"order"`
	Name      string    `json:"name"`
	Checksum  string    `json:"checksum"`
	AppliedAt time.Time `json:"appliedAt"`
}

// EntityDataMigrationStepReport 描述单个迁移的计划或执行状态。
type EntityDataMigrationStepReport struct {
	Scope    string `json:"scope"`
	Version  int64  `json:"version"`
	Order    int64  `json:"order"`
	Name     string `json:"name"`
	Checksum string `json:"checksum"`
	Status   string `json:"status"`
}

// EntityDataMigrationReport 汇总一次实体数据迁移。
type EntityDataMigrationReport struct {
	Namespace string                          `json:"namespace"`
	DryRun    bool                            `json:"dryRun"`
	Applied   int                             `json:"applied"`
	Skipped   int                             `json:"skipped"`
	Pending   int                             `json:"pending"`
	Steps     []EntityDataMigrationStepReport `json:"steps"`
}

// EntityMigrationState 是数据库当前已提交的 Entity 迁移版本快照。
// CurrentOrder 是该 Namespace 的全局版本，ScopeVersions 提供各 Entity 的独立版本。
type EntityMigrationState struct {
	Namespace     string           `json:"namespace"`
	CurrentOrder  int64            `json:"currentOrder"`
	AppliedCount  int              `json:"appliedCount"`
	ScopeVersions map[string]int64 `json:"scopeVersions"`
	LastAppliedAt *time.Time       `json:"lastAppliedAt,omitempty"`
}

// EntityDataMigrationTx 是业务迁移可使用的受控事务。
// 它不暴露 Commit/Rollback，也拒绝会触发隐式提交的 DDL，保证业务变更与迁移记录原子提交。
type EntityDataMigrationTx struct {
	tx *stdsql.Tx
}

// ExecContext 执行迁移 DML；DDL、事务控制和空 SQL 会被拒绝。
func (tx *EntityDataMigrationTx) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (stdsql.Result, error) {
	if tx == nil || tx.tx == nil {
		return nil, NewQueryException("实体数据迁移事务未初始化")
	}
	if err := validateEntityMigrationDML(query); err != nil {
		return nil, err
	}
	result, err := tx.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "执行实体数据迁移 DML 失败")
	}
	return result, nil
}

// QueryContext 执行迁移查询；调用方必须关闭返回的 Rows。
func (tx *EntityDataMigrationTx) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*stdsql.Rows, error) {
	if tx == nil || tx.tx == nil {
		return nil, NewQueryException("实体数据迁移事务未初始化")
	}
	if err := validateEntityMigrationQuery(query); err != nil {
		return nil, err
	}
	rows, err := tx.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "执行实体数据迁移查询失败")
	}
	return rows, nil
}

// EntityDataMigrationRow 封装受控单行查询及其前置校验错误。
type EntityDataMigrationRow struct {
	row *stdsql.Row
	err error
}

// Scan 扫描单行迁移查询；SQL 被安全策略拒绝时直接返回校验错误。
func (row *EntityDataMigrationRow) Scan(dest ...any) error {
	if row == nil {
		return NewQueryException("实体数据迁移单行查询未初始化")
	}
	if row.err != nil {
		return row.err
	}
	if row.row == nil {
		return NewQueryException("实体数据迁移单行查询未初始化")
	}
	if err := row.row.Scan(dest...); err != nil {
		return NewQueryExceptionWithCause(err, "扫描实体数据迁移单行查询失败")
	}
	return nil
}

// QueryRowContext 执行单行迁移查询。
func (tx *EntityDataMigrationTx) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *EntityDataMigrationRow {
	if tx == nil || tx.tx == nil {
		return &EntityDataMigrationRow{err: NewQueryException("实体数据迁移事务未初始化")}
	}
	if err := validateEntityMigrationQuery(query); err != nil {
		return &EntityDataMigrationRow{err: err}
	}
	return &EntityDataMigrationRow{row: tx.tx.QueryRowContext(ctx, query, args...)}
}

// ApplyEntityDataMigrations 校验并顺序执行程序化 Entity 数据迁移。
// 每个迁移及其审计记录在同一事务提交；跨实例通过 MySQL advisory lock 串行。
func (db *Db) ApplyEntityDataMigrations(
	ctx context.Context,
	migrations []EntityDataMigration,
	options *EntityDataMigrationOptions,
) (EntityDataMigrationReport, error) {
	config, definitions, err := normalizeEntityDataMigrations(migrations, options)
	report := EntityDataMigrationReport{
		Namespace: config.namespace,
		DryRun:    config.dryRun,
		Steps:     make([]EntityDataMigrationStepReport, 0, len(definitions)),
	}
	if err != nil {
		return report, err
	}
	if ctx == nil {
		return report, NewValidationException("context 不能为 nil")
	}
	if db == nil || db.DataSource == nil {
		return report, NewQueryException("数据库连接未初始化")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return report, NewQueryExceptionWithCause(ctxErr, "实体数据迁移上下文已结束")
	}

	lockConn, releaseLock, err := acquireEntityMigrationAdvisoryLock(
		ctx,
		db,
		config.namespace,
		config.lockTimeoutSeconds,
	)
	if err != nil {
		return report, err
	}
	defer releaseLock()

	releaseGeneration, err := lockSchemaDatabaseGeneration(ctx, db)
	if err != nil {
		return report, err
	}
	defer releaseGeneration()
	return db.applyEntityDataMigrationsOnConnection(ctx, lockConn, definitions, config, report)
}

// GetEntityMigrationState 返回指定 Namespace 当前已提交的数据库迁移版本。
// 尚未执行任何迁移或记录表尚未创建时返回零版本，不会创建数据库对象。
func (db *Db) GetEntityMigrationState(
	ctx context.Context,
	namespace string,
) (EntityMigrationState, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "default"
	}
	state := newEntityMigrationState(namespace)
	if err := validateEntityMigrationToken("namespace", namespace); err != nil {
		return state, err
	}
	if ctx == nil {
		return state, NewValidationException("context 不能为 nil")
	}
	if db == nil || db.DataSource == nil {
		return state, NewQueryException("数据库连接未初始化")
	}
	conn, err := db.DataSource.Conn(ctx)
	if err != nil {
		return state, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "获取实体迁移版本查询连接失败")
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			LogError("关闭实体迁移版本查询连接失败: namespace=%s err=%s", namespace, safeErrorForLog(closeErr))
		}
	}()
	exists, err := entityMigrationTableExists(ctx, conn)
	if err != nil || !exists {
		return state, err
	}
	records, err := readAppliedEntityMigrations(ctx, conn, namespace)
	if err != nil {
		return state, err
	}
	return buildEntityMigrationState(namespace, records), nil
}

type normalizedEntityDataMigrationOptions struct {
	namespace           string
	dryRun              bool
	lockTimeoutSeconds  int
	allowUnknownApplied bool
}

type normalizedEntityDataMigration struct {
	EntityDataMigration
	checksum string
}

func normalizeEntityDataMigrations(
	migrations []EntityDataMigration,
	options *EntityDataMigrationOptions,
) (normalizedEntityDataMigrationOptions, []normalizedEntityDataMigration, error) {
	config := normalizedEntityDataMigrationOptions{
		namespace:          "default",
		lockTimeoutSeconds: DefaultEntityMigrationLockTimeoutSeconds,
	}
	if options != nil {
		config.namespace = strings.TrimSpace(options.Namespace)
		config.dryRun = options.DryRun
		config.lockTimeoutSeconds = options.LockTimeoutSeconds
		config.allowUnknownApplied = options.AllowUnknownApplied
	}
	if config.namespace == "" {
		config.namespace = "default"
	}
	if err := validateEntityMigrationToken("namespace", config.namespace); err != nil {
		return config, nil, err
	}
	if config.lockTimeoutSeconds == 0 {
		config.lockTimeoutSeconds = DefaultEntityMigrationLockTimeoutSeconds
	}
	if config.lockTimeoutSeconds < 0 || config.lockTimeoutSeconds > 3600 {
		return config, nil, NewValidationException("实体迁移锁超时必须在 0~3600 秒")
	}

	normalized := make([]normalizedEntityDataMigration, 0, len(migrations))
	scopeVersionSet := make(map[string]struct{}, len(migrations))
	orderSet := make(map[int64]struct{}, len(migrations))
	for index, migration := range migrations {
		migration.Scope = strings.TrimSpace(migration.Scope)
		migration.Name = strings.TrimSpace(migration.Name)
		migration.Fingerprint = strings.TrimSpace(migration.Fingerprint)
		if err := validateEntityMigrationToken("scope", migration.Scope); err != nil {
			return config, nil, NewValidationExceptionWithCause(err, fmt.Sprintf("实体迁移定义非法: index=%d", index))
		}
		if err := validateEntityMigrationToken("name", migration.Name); err != nil {
			return config, nil, NewValidationExceptionWithCause(err, fmt.Sprintf("实体迁移定义非法: index=%d", index))
		}
		if migration.Version <= 0 || migration.Order <= 0 {
			return config, nil, NewValidationException(fmt.Sprintf(
				"实体迁移 Version/Order 必须为正数: index=%d scope=%s",
				index,
				migration.Scope,
			))
		}
		if migration.Fingerprint == "" || len(migration.Fingerprint) > 512 {
			return config, nil, NewValidationException(fmt.Sprintf(
				"实体迁移 Fingerprint 不能为空且最多 512 字符: index=%d scope=%s",
				index,
				migration.Scope,
			))
		}
		if migration.Up == nil {
			return config, nil, NewValidationException(fmt.Sprintf(
				"实体迁移 Up 回调不能为空: index=%d scope=%s",
				index,
				migration.Scope,
			))
		}
		scopeVersionKey := strings.ToLower(migration.Scope) + "\x00" + fmt.Sprint(migration.Version)
		if _, duplicate := scopeVersionSet[scopeVersionKey]; duplicate {
			return config, nil, NewValidationException(fmt.Sprintf(
				"实体迁移 Scope/Version 重复: scope=%s version=%d",
				migration.Scope,
				migration.Version,
			))
		}
		scopeVersionSet[scopeVersionKey] = struct{}{}
		if _, duplicate := orderSet[migration.Order]; duplicate {
			return config, nil, NewValidationException(fmt.Sprintf(
				"实体迁移 Order 重复: order=%d",
				migration.Order,
			))
		}
		orderSet[migration.Order] = struct{}{}
		normalized = append(normalized, normalizedEntityDataMigration{
			EntityDataMigration: migration,
			checksum:            entityMigrationChecksum(config.namespace, migration),
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Order < normalized[j].Order
	})
	return config, normalized, nil
}

func validateEntityMigrationToken(label, value string) error {
	if value == "" || len(value) > 128 {
		return NewValidationException(fmt.Sprintf("实体迁移 %s 不能为空且最多 128 字符", label))
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '_' || char == '-' || char == '.' || char == ':' {
			continue
		}
		return NewValidationException(fmt.Sprintf("实体迁移 %s 含非法字符", label))
	}
	return nil
}

func entityMigrationChecksum(namespace string, migration EntityDataMigration) string {
	payload := strings.Join([]string{
		namespace,
		migration.Scope,
		fmt.Sprint(migration.Version),
		fmt.Sprint(migration.Order),
		migration.Name,
		migration.Fingerprint,
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func validateEntityMigrationDML(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return NewValidationException("实体数据迁移 SQL 不能为空")
	}
	if mysqlMigrationHasImplicitCommit(query) {
		return NewValidationException("实体数据迁移回调禁止 DDL 或事务控制；请交由 schema 编排阶段执行")
	}
	switch sqlVerbForLog(query) {
	case "INSERT", "UPDATE", "DELETE", "REPLACE", "WITH":
	default:
		return NewValidationException("实体数据迁移 ExecContext 仅允许 INSERT/UPDATE/DELETE/REPLACE/WITH")
	}
	return nil
}

func validateEntityMigrationQuery(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return NewValidationException("实体数据迁移查询不能为空")
	}
	if mysqlMigrationHasImplicitCommit(query) || sqlVerbForLog(query) != "SELECT" {
		return NewValidationException("实体数据迁移 QueryContext/QueryRowContext 仅允许 SELECT")
	}
	return nil
}

func acquireEntityMigrationAdvisoryLock(
	ctx context.Context,
	db *Db,
	namespace string,
	timeoutSeconds int,
) (*stdsql.Conn, func(), error) {
	conn, err := db.DataSource.Conn(ctx)
	if err != nil {
		return nil, func() {}, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "获取实体迁移专用连接失败")
	}
	lockName, err := entityMigrationLockName(ctx, conn, namespace)
	if err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}
	var acquired stdsql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", lockName, timeoutSeconds).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, func() {}, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "获取实体迁移跨实例锁失败")
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		_ = conn.Close()
		return nil, func() {}, NewQueryException(fmt.Sprintf(
			"获取实体迁移跨实例锁超时: namespace=%s timeoutSeconds=%d",
			namespace,
			timeoutSeconds,
		))
	}
	return conn, func() {
		var released stdsql.NullInt64
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		releaseErr := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
		closeErr := conn.Close()
		if releaseErr != nil {
			LogError("释放实体迁移跨实例锁失败: namespace=%s err=%s", namespace, safeErrorForLog(releaseErr))
		}
		if closeErr != nil {
			LogError("关闭实体迁移专用连接失败: namespace=%s err=%s", namespace, safeErrorForLog(closeErr))
		}
	}, nil
}

func entityMigrationLockName(ctx context.Context, conn *stdsql.Conn, namespace string) (string, error) {
	var databaseName stdsql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
		return "", NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "读取实体迁移数据库名失败")
	}
	sum := sha256.Sum256([]byte(databaseName.String + "\x00" + namespace))
	return "db233:entity:" + hex.EncodeToString(sum[:])[:48], nil
}

func (db *Db) applyEntityDataMigrationsOnConnection(
	ctx context.Context,
	conn *stdsql.Conn,
	definitions []normalizedEntityDataMigration,
	config normalizedEntityDataMigrationOptions,
	report EntityDataMigrationReport,
) (EntityDataMigrationReport, error) {
	if config.dryRun {
		exists, err := entityMigrationTableExists(ctx, conn)
		if err != nil {
			return report, err
		}
		applied := []EntityDataMigrationRecord{}
		if exists {
			applied, err = readAppliedEntityMigrations(ctx, conn, config.namespace)
			if err != nil {
				return report, err
			}
		}
		if err := buildEntityMigrationPlan(definitions, applied, config, &report); err != nil {
			return report, err
		}
		if report.Pending > 0 {
			return report, fmt.Errorf("%w: namespace=%s pending=%d", ErrEntityMigrationPending, config.namespace, report.Pending)
		}
		return report, nil
	}

	if err := ensureEntityMigrationTable(ctx, conn); err != nil {
		return report, err
	}
	applied, err := readAppliedEntityMigrations(ctx, conn, config.namespace)
	if err != nil {
		return report, err
	}
	if err := buildEntityMigrationPlan(definitions, applied, config, &report); err != nil {
		return report, err
	}

	appliedKeys := make(map[string]struct{}, len(applied))
	for _, record := range applied {
		appliedKeys[entityMigrationKey(record.Scope, record.Version)] = struct{}{}
	}
	for _, definition := range definitions {
		if _, exists := appliedKeys[entityMigrationKey(definition.Scope, definition.Version)]; exists {
			continue
		}
		if err := applySingleEntityDataMigration(ctx, conn, config.namespace, definition); err != nil {
			return report, err
		}
		report.Applied++
		report.Pending--
		for index := range report.Steps {
			step := &report.Steps[index]
			if strings.EqualFold(step.Scope, definition.Scope) && step.Version == definition.Version {
				step.Status = "applied"
				break
			}
		}
	}
	return report, nil
}

func ensureEntityMigrationTable(ctx context.Context, conn *stdsql.Conn) error {
	statement := `
		CREATE TABLE IF NOT EXISTS db233_entity_migrations (
			namespace VARCHAR(128) NOT NULL,
			scope VARCHAR(128) NOT NULL,
			version BIGINT NOT NULL,
			order_value BIGINT NOT NULL,
			name VARCHAR(128) NOT NULL,
			checksum CHAR(64) NOT NULL,
			applied_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			PRIMARY KEY (namespace, scope, version),
			UNIQUE KEY uk_db233_entity_migration_order (namespace, order_value)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	if _, err := conn.ExecContext(ctx, statement); err != nil {
		return NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "创建实体迁移记录表失败")
	}
	return nil
}

func entityMigrationTableExists(ctx context.Context, conn *stdsql.Conn) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`,
		entityMigrationTableName,
	).Scan(&count)
	if err != nil {
		return false, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "检查实体迁移记录表失败")
	}
	return count > 0, nil
}

func readAppliedEntityMigrations(
	ctx context.Context,
	conn *stdsql.Conn,
	namespace string,
) (records []EntityDataMigrationRecord, resultErr error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT namespace, scope, version, order_value, name, checksum, applied_at
		FROM db233_entity_migrations
		WHERE namespace = ?
		ORDER BY order_value`,
		namespace,
	)
	if err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "读取实体迁移记录失败")
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			records = nil
			resultErr = errors.Join(resultErr, NewQueryExceptionWithCause(closeErr, "关闭实体迁移记录结果集失败"))
		}
	}()
	for rows.Next() {
		var record EntityDataMigrationRecord
		if err := rows.Scan(
			&record.Namespace,
			&record.Scope,
			&record.Version,
			&record.Order,
			&record.Name,
			&record.Checksum,
			&record.AppliedAt,
		); err != nil {
			return nil, NewQueryExceptionWithCause(err, "扫描实体迁移记录失败")
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "遍历实体迁移记录失败")
	}
	return records, nil
}

func newEntityMigrationState(namespace string) EntityMigrationState {
	return EntityMigrationState{
		Namespace:     namespace,
		ScopeVersions: make(map[string]int64),
	}
}

func buildEntityMigrationState(
	namespace string,
	records []EntityDataMigrationRecord,
) EntityMigrationState {
	state := newEntityMigrationState(namespace)
	state.AppliedCount = len(records)
	for index := range records {
		record := records[index]
		if record.Order > state.CurrentOrder {
			state.CurrentOrder = record.Order
		}
		if record.Version > state.ScopeVersions[record.Scope] {
			state.ScopeVersions[record.Scope] = record.Version
		}
		if state.LastAppliedAt == nil || record.AppliedAt.After(*state.LastAppliedAt) {
			appliedAt := record.AppliedAt
			state.LastAppliedAt = &appliedAt
		}
	}
	return state
}

func buildEntityMigrationPlan(
	definitions []normalizedEntityDataMigration,
	applied []EntityDataMigrationRecord,
	config normalizedEntityDataMigrationOptions,
	report *EntityDataMigrationReport,
) error {
	definitionsByKey := make(map[string]normalizedEntityDataMigration, len(definitions))
	maxAppliedOrder := int64(0)
	for _, definition := range definitions {
		definitionsByKey[entityMigrationKey(definition.Scope, definition.Version)] = definition
	}
	for _, record := range applied {
		if record.Order > maxAppliedOrder {
			maxAppliedOrder = record.Order
		}
		definition, exists := definitionsByKey[entityMigrationKey(record.Scope, record.Version)]
		if !exists {
			if config.allowUnknownApplied {
				continue
			}
			return fmt.Errorf(
				"%w: namespace=%s scope=%s version=%d",
				ErrUnknownAppliedEntityMigration,
				config.namespace,
				record.Scope,
				record.Version,
			)
		}
		if record.Name != definition.Name ||
			record.Order != definition.Order ||
			!strings.EqualFold(record.Checksum, definition.checksum) {
			return fmt.Errorf(
				"%w: namespace=%s scope=%s version=%d",
				ErrEntityMigrationDefinitionChanged,
				config.namespace,
				record.Scope,
				record.Version,
			)
		}
	}

	appliedByKey := make(map[string]EntityDataMigrationRecord, len(applied))
	for _, record := range applied {
		appliedByKey[entityMigrationKey(record.Scope, record.Version)] = record
	}
	for _, definition := range definitions {
		step := EntityDataMigrationStepReport{
			Scope:    definition.Scope,
			Version:  definition.Version,
			Order:    definition.Order,
			Name:     definition.Name,
			Checksum: definition.checksum,
		}
		if _, exists := appliedByKey[entityMigrationKey(definition.Scope, definition.Version)]; exists {
			step.Status = "skipped"
			report.Skipped++
		} else {
			if definition.Order <= maxAppliedOrder {
				return NewValidationException(fmt.Sprintf(
					"禁止在已应用迁移之前插入新迁移: namespace=%s scope=%s version=%d order=%d maxAppliedOrder=%d",
					config.namespace,
					definition.Scope,
					definition.Version,
					definition.Order,
					maxAppliedOrder,
				))
			}
			step.Status = "pending"
			report.Pending++
		}
		report.Steps = append(report.Steps, step)
	}
	return nil
}

func applySingleEntityDataMigration(
	ctx context.Context,
	conn *stdsql.Conn,
	namespace string,
	definition normalizedEntityDataMigration,
) (resultErr error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "开启实体数据迁移事务失败")
	}
	defer func() {
		if resultErr != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, stdsql.ErrTxDone) {
				resultErr = errors.Join(resultErr, NewQueryExceptionWithCause(rollbackErr, "回滚实体数据迁移失败"))
			}
		}
	}()
	controlledTx := &EntityDataMigrationTx{tx: tx}
	if err := invokeEntityMigrationCallback(ctx, definition, "up", definition.Up, controlledTx); err != nil {
		return err
	}
	if definition.Verify != nil {
		if err := invokeEntityMigrationCallback(ctx, definition, "verify", definition.Verify, controlledTx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO db233_entity_migrations
			(namespace, scope, version, order_value, name, checksum)
		VALUES (?, ?, ?, ?, ?, ?)`,
		namespace,
		definition.Scope,
		definition.Version,
		definition.Order,
		definition.Name,
		definition.checksum,
	); err != nil {
		return NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "写入实体迁移审计记录失败")
	}
	if err := tx.Commit(); err != nil {
		return NewQueryExceptionWithCause(joinErrorWithContext(err, ctx), "提交实体数据迁移失败")
	}
	LogInfo(
		"实体数据迁移成功: namespace=%s scope=%s version=%d order=%d name=%s",
		namespace,
		definition.Scope,
		definition.Version,
		definition.Order,
		definition.Name,
	)
	return nil
}

func invokeEntityMigrationCallback(
	ctx context.Context,
	definition normalizedEntityDataMigration,
	phase string,
	callback EntityDataMigrationFunc,
	tx *EntityDataMigrationTx,
) (resultErr error) {
	defer func() {
		if recover() != nil {
			resultErr = NewQueryException(fmt.Sprintf(
				"实体迁移业务回调异常: scope=%s version=%d phase=%s",
				definition.Scope,
				definition.Version,
				phase,
			))
		}
	}()
	if err := callback(ctx, tx); err != nil {
		return fmt.Errorf(
			"实体迁移失败: scope=%s version=%d phase=%s: %w",
			definition.Scope,
			definition.Version,
			phase,
			err,
		)
	}
	return nil
}

func entityMigrationKey(scope string, version int64) string {
	return strings.ToLower(scope) + "\x00" + fmt.Sprint(version)
}
