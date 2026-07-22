package db233

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultSchemaConcurrency 是 schema 编排默认并发度。DDL 仅跨表并发，
	// 同一张表内的语句始终按计划顺序执行。
	DefaultSchemaConcurrency = 4
	// MaxSchemaConcurrency 防止错误配置一次创建过多数据库连接。
	MaxSchemaConcurrency = 32
)

// ErrSchemaVerificationFailed 表示 metadata 已成功读取，但实际 schema 未满足
// VerifySchema 的兼容性要求。调用方仍可检查同时返回的完整 report。
var ErrSchemaVerificationFailed = errors.New("db233: schema verification failed")

// SchemaMigrationPermissions 明确控制 schema 编排可以执行的操作。
// 零值拒绝全部变更；SchemaMigrationOptions.Permissions 为 nil 时使用
// DefaultSchemaMigrationPermissions（只允许安全的增量操作）。
type SchemaMigrationPermissions struct {
	CreateTable  bool `json:"createTable"`
	CreateColumn bool `json:"createColumn"`
	CreateIndex  bool `json:"createIndex"`
	UpdateColumn bool `json:"updateColumn"`
	DeleteColumn bool `json:"deleteColumn"`
	ReplaceIndex bool `json:"replaceIndex"`
	DeleteIndex  bool `json:"deleteIndex"`
}

// DefaultSchemaMigrationPermissions 返回生产安全默认值：只建表、增列和增索引。
func DefaultSchemaMigrationPermissions() SchemaMigrationPermissions {
	return SchemaMigrationPermissions{
		CreateTable:  true,
		CreateColumn: true,
		CreateIndex:  true,
	}
}

// SchemaMigrationOptions 控制批量 schema 迁移。
type SchemaMigrationOptions struct {
	MaxConcurrency int                         `json:"maxConcurrency"`
	DryRun         bool                        `json:"dryRun"`
	Permissions    *SchemaMigrationPermissions `json:"permissions,omitempty"`
}

// SchemaVerifyOptions 控制只读 schema 验证。
type SchemaVerifyOptions struct {
	MaxConcurrency int `json:"maxConcurrency"`
	// RequireExact 除兼容性外，还要求数据库不存在实体未声明的列或索引。
	RequireExact bool `json:"requireExact"`
}

// SchemaIssueKind 是稳定、可机器判断的 schema 漂移类型。
type SchemaIssueKind string

const (
	SchemaIssueMissingTable        SchemaIssueKind = "missing_table"
	SchemaIssueMissingColumn       SchemaIssueKind = "missing_column"
	SchemaIssueColumnType          SchemaIssueKind = "column_type_mismatch"
	SchemaIssueColumnNullability   SchemaIssueKind = "column_nullability_mismatch"
	SchemaIssueColumnAutoIncrement SchemaIssueKind = "column_auto_increment_mismatch"
	SchemaIssuePrimaryKey          SchemaIssueKind = "primary_key_mismatch"
	SchemaIssueExtraColumn         SchemaIssueKind = "extra_column"
	SchemaIssueMissingIndex        SchemaIssueKind = "missing_index"
	SchemaIssueIndexDefinition     SchemaIssueKind = "index_definition_mismatch"
	SchemaIssueExtraIndex          SchemaIssueKind = "extra_index"
	SchemaIssuePermissionBlocked   SchemaIssueKind = "permission_blocked"
)

// SchemaIssue 描述一个只读验证发现。BlockedBy 仅用于迁移计划，说明某个漂移
// 因安全权限未开启而不会被自动修改。
type SchemaIssue struct {
	Kind      SchemaIssueKind `json:"kind"`
	TableName string          `json:"tableName"`
	Object    string          `json:"object,omitempty"`
	Expected  string          `json:"expected,omitempty"`
	Actual    string          `json:"actual,omitempty"`
	BlockedBy string          `json:"blockedBy,omitempty"`
}

// SchemaTableVerification 是单表只读验证结果。
type SchemaTableVerification struct {
	TableName  string        `json:"tableName"`
	EntityType string        `json:"entityType"`
	Exists     bool          `json:"exists"`
	Compatible bool          `json:"compatible"`
	Exact      bool          `json:"exact"`
	Issues     []SchemaIssue `json:"issues,omitempty"`
}

// SchemaVerificationReport 的 Compatible 表示所有实体要求的结构均已满足；
// Exact 还要求不存在实体未声明的列或索引。
type SchemaVerificationReport struct {
	Compatible bool                      `json:"compatible"`
	Exact      bool                      `json:"exact"`
	Tables     []SchemaTableVerification `json:"tables"`
}

// SchemaActionKind 是稳定的迁移动作类型。
type SchemaActionKind string

const (
	SchemaActionCreateTable  SchemaActionKind = "create_table"
	SchemaActionCreateColumn SchemaActionKind = "create_column"
	SchemaActionUpdateColumn SchemaActionKind = "update_column"
	SchemaActionDeleteColumn SchemaActionKind = "delete_column"
	SchemaActionCreateIndex  SchemaActionKind = "create_index"
	SchemaActionDeleteIndex  SchemaActionKind = "delete_index"
)

// SchemaMigrationAction 是一条已验证并稳定排序的 DDL 计划。
type SchemaMigrationAction struct {
	Kind      SchemaActionKind `json:"kind"`
	TableName string           `json:"tableName"`
	Object    string           `json:"object,omitempty"`
	Statement string           `json:"statement"`
}

// SchemaTableMigrationReport 是单表的完整计划、执行结果与被权限阻止的漂移。
type SchemaTableMigrationReport struct {
	TableName     string                  `json:"tableName"`
	EntityType    string                  `json:"entityType"`
	Actions       []SchemaMigrationAction `json:"actions,omitempty"`
	Executed      int                     `json:"executed"`
	BlockedIssues []SchemaIssue           `json:"blockedIssues,omitempty"`
}

// SchemaMigrationReport 同时保留执行前与执行后的权威数据库快照。
// DryRun 时不执行 DDL，After 与 Before 相同。
type SchemaMigrationReport struct {
	DryRun bool                         `json:"dryRun"`
	Tables []SchemaTableMigrationReport `json:"tables"`
	Before SchemaVerificationReport     `json:"before"`
	After  SchemaVerificationReport     `json:"after"`
}

// IContextTableCreationStrategy 是生产 schema 编排要求的 context-aware 扩展。
// 自定义策略必须实现它，避免取消后仍在后台执行不受控的元数据查询。
type IContextTableCreationStrategy interface {
	ITableCreationStrategy
	TableExistsContext(context.Context, *Db, string) (bool, error)
	GetTableColumnsContext(context.Context, *Db, string) (map[string]ColumnInfo, error)
	GetExistingIndexesContext(context.Context, *Db, string) (map[string]*IndexMetaData, error)
}

type schemaEntitySpec struct {
	prototype  any
	entityType reflect.Type
	entityName string
	tableName  string
	uidColumn  string
	columns    map[string]reflect.StructField
	indexes    map[string]*IndexMetaData
}

type schemaObservedTable struct {
	exists  bool
	columns map[string]ColumnInfo
	indexes map[string]*IndexMetaData
}

type schemaDatabaseLock struct {
	token chan struct{}
	refs  int
}

var schemaDatabaseLocks = struct {
	sync.Mutex
	entries map[*Db]*schemaDatabaseLock
}{entries: make(map[*Db]*schemaDatabaseLock)}

var schemaIntegerDisplayWidthPattern = regexp.MustCompile(`\b(bigint|int|mediumint|smallint|tinyint)\([0-9]+\)`)
var schemaBoolPattern = regexp.MustCompile(`\bbool\b`)
var schemaSQLTypePattern = regexp.MustCompile(`(?i)^(tinyint|smallint|mediumint|int|integer|bigint|decimal|numeric|float|double|real|char|varchar|binary|varbinary|tinytext|text|mediumtext|longtext|tinyblob|blob|mediumblob|longblob|date|datetime|timestamp|time|year|json|boolean|bool)(\([0-9]+(,[0-9]+)?\))?( (unsigned|zerofill)){0,2}$`)

// AutoMigrateSchema 以单个 Db generation 租约批量规划并迁移实体 schema。
// 不依赖任何业务 registry；重复的同类型原型会去重，不同 Go 类型映射到同一
// 表名会在访问数据库前失败。
func (db *Db) AutoMigrateSchema(
	ctx context.Context,
	entities []any,
	options *SchemaMigrationOptions,
) (report SchemaMigrationReport, err error) {
	if ctx == nil {
		return report, NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return report, NewQueryExceptionWithCause(ctxErr, "schema 迁移上下文已结束")
	}
	if db == nil || db.DataSource == nil {
		return report, NewQueryException("数据库连接未初始化")
	}

	config, configErr := normalizeSchemaMigrationOptions(options)
	if configErr != nil {
		return report, configErr
	}
	report.DryRun = config.dryRun
	specs, specErr := buildSchemaEntitySpecs(ctx, db, entities)
	if specErr != nil {
		return report, specErr
	}

	releaseOrchestration, lockErr := acquireSchemaDatabaseLock(ctx, db)
	if lockErr != nil {
		return report, lockErr
	}
	defer releaseOrchestration()
	releaseGeneration, generationErr := lockSchemaDatabaseGeneration(ctx, db)
	if generationErr != nil {
		return report, generationErr
	}
	defer releaseGeneration()

	strategy, strategyErr := contextSchemaStrategy(db)
	if strategyErr != nil {
		return report, strategyErr
	}
	if validationErr := validateSchemaSpecsForStrategy(strategy, specs); validationErr != nil {
		return report, validationErr
	}
	observed, observeErr := observeSchemaTables(ctx, db, strategy, specs, config.maxConcurrency)
	if observeErr != nil {
		return report, observeErr
	}
	report.Before = buildSchemaVerificationReport(strategy, specs, observed)
	report.Tables, err = buildSchemaMigrationPlan(strategy, specs, observed, config.permissions)
	if err != nil {
		return report, err
	}
	if config.dryRun {
		report.After = cloneSchemaVerificationReport(report.Before)
		return report, nil
	}

	executionErr := executeSchemaMigrationPlan(ctx, db, strategy, specs, report.Tables, config.maxConcurrency)
	// MySQL DDL 不能可靠回滚。即使某条语句失败，只要原 context 仍可用，也要
	// 重新读取权威 schema，让调用方准确看到已经提交的部分动作。
	if contextCauseError(ctx) == nil {
		finalObserved, finalErr := observeSchemaTables(ctx, db, strategy, specs, config.maxConcurrency)
		if finalErr == nil {
			report.After = buildSchemaVerificationReport(strategy, specs, finalObserved)
			if !report.After.Compatible {
				executionErr = errors.Join(executionErr, fmt.Errorf(
					"%w: 自动迁移后 schema 仍不兼容",
					ErrSchemaVerificationFailed,
				))
			}
		} else {
			executionErr = errors.Join(executionErr, finalErr)
		}
	}
	return report, executionErr
}

// VerifySchema 只读取数据库 metadata，不执行 DDL。默认要求 Compatible；
// RequireExact=true 时要求 Exact。漂移会同时返回完整 report 和
// ErrSchemaVerificationFailed，便于 CI/启动检查严格失败。
func (db *Db) VerifySchema(
	ctx context.Context,
	entities []any,
	options *SchemaVerifyOptions,
) (SchemaVerificationReport, error) {
	var report SchemaVerificationReport
	if ctx == nil {
		return report, NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return report, NewQueryExceptionWithCause(ctxErr, "schema 验证上下文已结束")
	}
	if db == nil || db.DataSource == nil {
		return report, NewQueryException("数据库连接未初始化")
	}
	maxConcurrency, err := normalizeSchemaConcurrency(0)
	if options != nil {
		maxConcurrency, err = normalizeSchemaConcurrency(options.MaxConcurrency)
	}
	if err != nil {
		return report, err
	}
	specs, err := buildSchemaEntitySpecs(ctx, db, entities)
	if err != nil {
		return report, err
	}

	releaseOrchestration, err := acquireSchemaDatabaseLock(ctx, db)
	if err != nil {
		return report, err
	}
	defer releaseOrchestration()
	releaseGeneration, err := lockSchemaDatabaseGeneration(ctx, db)
	if err != nil {
		return report, err
	}
	defer releaseGeneration()
	strategy, err := contextSchemaStrategy(db)
	if err != nil {
		return report, err
	}
	if err := validateSchemaSpecsForStrategy(strategy, specs); err != nil {
		return report, err
	}
	observed, err := observeSchemaTables(ctx, db, strategy, specs, maxConcurrency)
	if err != nil {
		return report, err
	}
	report = buildSchemaVerificationReport(strategy, specs, observed)
	requireExact := options != nil && options.RequireExact
	if !report.Compatible || (requireExact && !report.Exact) {
		return report, fmt.Errorf(
			"%w: compatible=%t, exact=%t, requireExact=%t",
			ErrSchemaVerificationFailed, report.Compatible, report.Exact, requireExact,
		)
	}
	return report, nil
}

type normalizedSchemaMigrationOptions struct {
	maxConcurrency int
	dryRun         bool
	permissions    SchemaMigrationPermissions
}

func normalizeSchemaMigrationOptions(options *SchemaMigrationOptions) (normalizedSchemaMigrationOptions, error) {
	maxConcurrency := 0
	dryRun := false
	permissions := DefaultSchemaMigrationPermissions()
	if options != nil {
		maxConcurrency = options.MaxConcurrency
		dryRun = options.DryRun
		if options.Permissions != nil {
			permissions = *options.Permissions
		}
	}
	maxConcurrency, err := normalizeSchemaConcurrency(maxConcurrency)
	if err != nil {
		return normalizedSchemaMigrationOptions{}, err
	}
	return normalizedSchemaMigrationOptions{
		maxConcurrency: maxConcurrency,
		dryRun:         dryRun,
		permissions:    permissions,
	}, nil
}

func normalizeSchemaConcurrency(value int) (int, error) {
	if value < 0 {
		return 0, NewValidationException("schema 并发度不能为负数")
	}
	if value == 0 {
		return DefaultSchemaConcurrency, nil
	}
	if value > MaxSchemaConcurrency {
		return 0, NewValidationException(fmt.Sprintf(
			"schema 并发度 %d 超过生产上限 %d", value, MaxSchemaConcurrency,
		))
	}
	return value, nil
}

func acquireSchemaDatabaseLock(ctx context.Context, db *Db) (func(), error) {
	schemaDatabaseLocks.Lock()
	entry := schemaDatabaseLocks.entries[db]
	if entry == nil {
		entry = &schemaDatabaseLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		schemaDatabaseLocks.entries[db] = entry
	}
	entry.refs++
	schemaDatabaseLocks.Unlock()

	select {
	case <-ctx.Done():
		releaseSchemaDatabaseLockReference(db, entry)
		return func() {}, NewQueryExceptionWithCause(contextCauseError(ctx), "等待 schema 编排锁失败")
	case <-entry.token:
		var once sync.Once
		return func() {
			once.Do(func() {
				entry.token <- struct{}{}
				releaseSchemaDatabaseLockReference(db, entry)
			})
		}, nil
	}
}

func releaseSchemaDatabaseLockReference(db *Db, entry *schemaDatabaseLock) {
	schemaDatabaseLocks.Lock()
	entry.refs--
	if entry.refs == 0 && schemaDatabaseLocks.entries[db] == entry {
		delete(schemaDatabaseLocks.entries, db)
	}
	schemaDatabaseLocks.Unlock()
}

func lockSchemaDatabaseGeneration(ctx context.Context, db *Db) (func(), error) {
	db.resourceMu.Lock()
	closing := db.closing || db.closingState.Load()
	db.resourceMu.Unlock()
	if closing {
		return func() {}, ErrCrudRepositoryClosed
	}
	_, release, err := db.lockCurrentDatabaseGenerationContext(ctx)
	return release, err
}

func contextSchemaStrategy(db *Db) (IContextTableCreationStrategy, error) {
	strategy := GetStrategyFactoryInstance().GetStrategy(db.DatabaseType)
	if strategy == nil {
		return nil, NewConfigurationException("未找到可用的建表策略")
	}
	requestedType := db.DatabaseType
	if requestedType == "" || !requestedType.IsValid() {
		requestedType = EnumDatabaseTypeMySQL
	}
	if strategy.GetDatabaseType() != requestedType {
		return nil, NewConfigurationException(fmt.Sprintf(
			"schema 策略类型不匹配: Db=%s, Strategy=%s",
			requestedType,
			strategy.GetDatabaseType(),
		))
	}
	contextStrategy, ok := strategy.(IContextTableCreationStrategy)
	if !ok {
		return nil, NewConfigurationException(fmt.Sprintf(
			"schema 策略 %T 未实现 IContextTableCreationStrategy", strategy,
		))
	}
	return contextStrategy, nil
}

func buildSchemaEntitySpecs(ctx context.Context, db *Db, entities []any) ([]schemaEntitySpec, error) {
	if len(entities) == 0 {
		return []schemaEntitySpec{}, nil
	}
	cm := GetCrudManagerInstance()
	specByTable := make(map[string]schemaEntitySpec, len(entities))
	for index, prototype := range entities {
		if ctxErr := contextCauseError(ctx); ctxErr != nil {
			return nil, NewQueryExceptionWithCause(ctxErr, fmt.Sprintf("解析 schema 实体中断: index=%d", index))
		}
		spec, err := buildSchemaEntitySpecSafely(cm, db, prototype, index)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(spec.tableName)
		if existing, ok := specByTable[key]; ok {
			if existing.entityType != spec.entityType {
				return nil, NewValidationException(fmt.Sprintf(
					"不同实体映射到同一张表 %s: %s 与 %s",
					spec.tableName, existing.entityType, spec.entityType,
				))
			}
			continue
		}
		specByTable[key] = spec
	}
	specs := make([]schemaEntitySpec, 0, len(specByTable))
	for _, spec := range specByTable {
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].tableName < specs[j].tableName
	})
	return specs, nil
}

func buildSchemaEntitySpecSafely(
	cm *CrudManager,
	db *Db,
	prototype any,
	index int,
) (spec schemaEntitySpec, err error) {
	// 仅在用户可实现的 metadata 边界恢复：TableName/GetTableMetaData 的 panic
	// 不能击穿生产启动；不暴露 panic 值，避免其中携带凭据或业务数据。
	defer func() {
		if recover() != nil {
			spec = schemaEntitySpec{}
			err = NewValidationException(fmt.Sprintf(
				"schema 实体 metadata 回调异常: index=%d, type=%T",
				index, prototype,
			))
		}
	}()

	entityType, tableName, err := validateCrudMigrationInput(cm, db, prototype)
	if err != nil {
		return spec, NewValidationExceptionWithCause(err, fmt.Sprintf("schema 实体非法: index=%d", index))
	}
	if err := validateRepositoryTableIdentifier(tableName); err != nil {
		return spec, NewValidationExceptionWithCause(err, fmt.Sprintf("schema 表名非法: index=%d", index))
	}
	columns := cm.getEntityColumns(entityType)
	if len(columns) == 0 {
		return spec, NewValidationException(fmt.Sprintf("schema 实体 %s 没有 db 列", entityType))
	}
	columnNames := make([]string, 0, len(columns))
	columnNamesByKey := make(map[string]string, len(columns))
	for name := range columns {
		key := strings.ToLower(name)
		if existing, duplicate := columnNamesByKey[key]; duplicate {
			return spec, NewValidationException(fmt.Sprintf(
				"schema 列名大小写冲突: table=%s, columns=%s,%s",
				tableName, existing, name,
			))
		}
		columnNamesByKey[key] = name
		columnNames = append(columnNames, name)
	}
	sort.Strings(columnNames)
	uidColumn := cm.GetPrimaryKeyColumnName(prototype)
	if uidColumn == "" {
		uidColumn = "id"
	}
	if err := validateRepositorySQLIdentifiers(tableName, uidColumn, columnNames); err != nil {
		return spec, NewValidationExceptionWithCause(err, fmt.Sprintf("schema 标识符非法: index=%d", index))
	}
	indexes, err := schemaExpectedIndexes(prototype, entityType, tableName, columns)
	if err != nil {
		return spec, NewValidationExceptionWithCause(err, fmt.Sprintf("schema 索引非法: index=%d", index))
	}
	return schemaEntitySpec{
		prototype:  prototype,
		entityType: entityType,
		entityName: entityType.String(),
		tableName:  tableName,
		uidColumn:  uidColumn,
		columns:    columns,
		indexes:    indexes,
	}, nil
}

func schemaExpectedIndexes(
	prototype any,
	entityType reflect.Type,
	tableName string,
	columns map[string]reflect.StructField,
) (map[string]*IndexMetaData, error) {
	var provider ITableMetaDataProvider
	if candidate, ok := prototype.(ITableMetaDataProvider); ok && !isNilStrictValue(candidate) {
		provider = candidate
	} else {
		pointer := reflect.New(entityType).Interface()
		if candidate, ok := pointer.(ITableMetaDataProvider); ok {
			provider = candidate
		}
	}
	if provider == nil {
		return map[string]*IndexMetaData{}, nil
	}
	metadata := provider.GetTableMetaData()
	if metadata == nil {
		return map[string]*IndexMetaData{}, nil
	}
	if metadata.TableName != "" {
		if err := validateRepositoryTableIdentifier(metadata.TableName); err != nil {
			return nil, fmt.Errorf("索引 metadata 表名非法: %w", err)
		}
	}
	if metadata.TableName != "" && !strings.EqualFold(metadata.TableName, tableName) {
		return nil, fmt.Errorf("索引 metadata 表名 %s 与实体表名 %s 不一致", metadata.TableName, tableName)
	}
	indexes := make(map[string]*IndexMetaData, len(metadata.Indexes))
	for index, expected := range metadata.Indexes {
		if expected == nil || expected.IndexName == "" || len(expected.Columns) == 0 {
			return nil, fmt.Errorf("索引 metadata 无效: index=%d", index)
		}
		if err := validateRepositoryColumnIdentifier(expected.IndexName); err != nil {
			return nil, fmt.Errorf("索引名非法 %s: %w", expected.IndexName, err)
		}
		if strings.EqualFold(expected.IndexName, "PRIMARY") {
			return nil, fmt.Errorf("索引名不能使用保留主键名 PRIMARY")
		}
		key := strings.ToLower(expected.IndexName)
		if _, exists := indexes[key]; exists {
			return nil, fmt.Errorf("索引名重复: %s", expected.IndexName)
		}
		cloned := &IndexMetaData{
			IndexName: expected.IndexName,
			Columns:   append([]string(nil), expected.Columns...),
			IsUnique:  expected.IsUnique,
		}
		indexColumnSet := make(map[string]struct{}, len(cloned.Columns))
		for _, column := range cloned.Columns {
			if err := validateRepositoryColumnIdentifier(column); err != nil {
				return nil, fmt.Errorf("索引 %s 列名非法: %w", cloned.IndexName, err)
			}
			columnKey := strings.ToLower(column)
			if _, duplicate := indexColumnSet[columnKey]; duplicate {
				return nil, fmt.Errorf("索引 %s 重复引用列 %s", cloned.IndexName, column)
			}
			indexColumnSet[columnKey] = struct{}{}
			if _, ok := schemaColumnFieldByName(columns, column); !ok {
				return nil, fmt.Errorf("索引 %s 引用了实体未声明的列 %s", cloned.IndexName, column)
			}
		}
		indexes[key] = cloned
	}
	return indexes, nil
}

func schemaColumnFieldByName(
	columns map[string]reflect.StructField,
	columnName string,
) (reflect.StructField, bool) {
	if field, exists := columns[columnName]; exists {
		return field, true
	}
	for name, field := range columns {
		if strings.EqualFold(name, columnName) {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func validateSchemaSpecsForStrategy(strategy ITableCreationStrategy, specs []schemaEntitySpec) error {
	for _, spec := range specs {
		for columnName, field := range spec.columns {
			sqlType := strings.ToLower(strings.Join(strings.Fields(strategy.GetSQLType(field)), " "))
			sqlType = strings.ReplaceAll(sqlType, "( ", "(")
			sqlType = strings.ReplaceAll(sqlType, " )", ")")
			sqlType = strings.ReplaceAll(sqlType, ", ", ",")
			if !schemaSQLTypePattern.MatchString(sqlType) {
				return NewValidationException(fmt.Sprintf(
					"schema SQL 类型声明不安全: 表=%s, 列=%s, Type=%s",
					spec.tableName, columnName, safeValueForLog(sqlType),
				))
			}
			if schemaExpectedAutoIncrement(field) {
				if !schemaExpectedPrimary(spec, columnName, field) {
					return NewValidationException(fmt.Sprintf(
						"AUTO_INCREMENT 列必须是主键: table=%s, column=%s",
						spec.tableName, columnName,
					))
				}
				baseType := strings.Fields(normalizeSchemaSQLType(sqlType))[0]
				if parenthesis := strings.IndexByte(baseType, '('); parenthesis >= 0 {
					baseType = baseType[:parenthesis]
				}
				switch baseType {
				case "tinyint", "smallint", "mediumint", "int", "bigint":
				default:
					return NewValidationException(fmt.Sprintf(
						"AUTO_INCREMENT 列必须使用整数 SQL 类型: table=%s, column=%s",
						spec.tableName, columnName,
					))
				}
			}
		}
	}
	return nil
}

func observeSchemaTables(
	ctx context.Context,
	db *Db,
	strategy IContextTableCreationStrategy,
	specs []schemaEntitySpec,
	maxConcurrency int,
) ([]schemaObservedTable, error) {
	observed := make([]schemaObservedTable, len(specs))
	if len(specs) == 0 {
		return observed, nil
	}
	workerCount := maxConcurrency
	if workerCount > len(specs) {
		workerCount = len(specs)
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, len(specs))
	for index := range specs {
		jobs <- index
	}
	close(jobs)
	type observationResult struct {
		index int
		value schemaObservedTable
		err   error
	}
	results := make(chan observationResult, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if jobCtx.Err() != nil {
					return
				}
				value, err := observeSchemaTable(jobCtx, db, strategy, specs[index].tableName)
				results <- observationResult{index: index, value: value, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	var observationErrors []error
	for result := range results {
		if result.err != nil {
			observationErrors = append(observationErrors, fmt.Errorf(
				"读取表 %s schema: %w", specs[result.index].tableName, result.err,
			))
			continue
		}
		observed[result.index] = result.value
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		observationErrors = append(observationErrors, ctxErr)
	}
	return observed, errors.Join(observationErrors...)
}

func observeSchemaTable(
	ctx context.Context,
	db *Db,
	strategy IContextTableCreationStrategy,
	tableName string,
) (schemaObservedTable, error) {
	result := schemaObservedTable{
		columns: make(map[string]ColumnInfo),
		indexes: make(map[string]*IndexMetaData),
	}
	exists, err := strategy.TableExistsContext(ctx, db, tableName)
	if err != nil {
		return result, err
	}
	result.exists = exists
	if !exists {
		return result, nil
	}
	columns, err := strategy.GetTableColumnsContext(ctx, db, tableName)
	if err != nil {
		return result, err
	}
	indexes, err := strategy.GetExistingIndexesContext(ctx, db, tableName)
	if err != nil {
		return result, err
	}
	result.columns = columns
	result.indexes = indexes
	return result, nil
}

func buildSchemaMigrationPlan(
	strategy ITableCreationStrategy,
	specs []schemaEntitySpec,
	observed []schemaObservedTable,
	permissions SchemaMigrationPermissions,
) ([]SchemaTableMigrationReport, error) {
	reports := make([]SchemaTableMigrationReport, 0, len(specs))
	for specIndex, spec := range specs {
		actual := schemaObservedTable{}
		if specIndex < len(observed) {
			actual = observed[specIndex]
		}
		verification := verifySchemaTable(strategy, spec, actual)
		tableReport := SchemaTableMigrationReport{
			TableName:  spec.tableName,
			EntityType: spec.entityName,
		}
		if !actual.exists {
			if !permissions.CreateTable {
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(verification.Issues[0], "createTable"),
				)
				reports = append(reports, tableReport)
				continue
			}
			statement, err := strategy.GenerateCreateTableSQL(spec.tableName, spec.entityType, spec.uidColumn)
			if err != nil {
				return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
					"生成建表计划失败: table=%s", spec.tableName,
				))
			}
			if err := appendSchemaMigrationAction(&tableReport, SchemaActionCreateTable, "", statement); err != nil {
				return nil, err
			}
			for _, key := range sortedSchemaIndexKeys(spec.indexes) {
				expected := spec.indexes[key]
				if !permissions.CreateIndex {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues, blockedSchemaIssue(SchemaIssue{
						Kind: SchemaIssueMissingIndex, TableName: spec.tableName, Object: expected.IndexName,
						Expected: schemaIndexSummary(expected), Actual: "missing",
					}, "createIndex"))
					continue
				}
				statement, err := strategy.GenerateCreateIndexSQL(spec.tableName, expected)
				if err != nil {
					return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
						"生成索引计划失败: table=%s, index=%s", spec.tableName, expected.IndexName,
					))
				}
				if err := appendSchemaMigrationAction(&tableReport, SchemaActionCreateIndex, expected.IndexName, statement); err != nil {
					return nil, err
				}
			}
			sortSchemaIssues(tableReport.BlockedIssues)
			reports = append(reports, tableReport)
			continue
		}

		actualColumns, actualColumnNames := normalizedSchemaColumns(actual.columns)
		expectedColumnNames := make([]string, 0, len(spec.columns))
		for name := range spec.columns {
			expectedColumnNames = append(expectedColumnNames, name)
		}
		sort.Strings(expectedColumnNames)
		for _, name := range expectedColumnNames {
			field := spec.columns[name]
			column, exists := actualColumns[strings.ToLower(name)]
			if !exists {
				issue := findSchemaIssue(verification.Issues, SchemaIssueMissingColumn, name)
				if schemaExpectedPrimary(spec, name, field) {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues,
						blockedSchemaIssue(issue, "primaryKeyChange"),
					)
					continue
				}
				if !permissions.CreateColumn {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues,
						blockedSchemaIssue(issue, "createColumn"),
					)
					continue
				}
				statement, err := strategy.GenerateAddColumnSQL(spec.tableName, field, name)
				if err != nil {
					return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
						"生成增列计划失败: table=%s, column=%s", spec.tableName, name,
					))
				}
				if err := appendSchemaMigrationAction(&tableReport, SchemaActionCreateColumn, name, statement); err != nil {
					return nil, err
				}
				continue
			}

			typeMismatch := !schemaSQLTypesCompatible(column.Type, strategy.GetSQLType(field))
			nullMismatch := column.IsNullable != schemaExpectedNullable(spec, name, field)
			autoIncrementMismatch := schemaColumnIsAutoIncrement(column) != schemaExpectedAutoIncrement(field)
			primaryMismatch := column.IsPrimary != schemaExpectedPrimary(spec, name, field)
			if primaryMismatch {
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(findSchemaIssue(verification.Issues, SchemaIssuePrimaryKey, name), "primaryKeyChange"),
				)
			}
			if !typeMismatch && !nullMismatch && !autoIncrementMismatch {
				continue
			}
			if !permissions.UpdateColumn {
				if typeMismatch {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues,
						blockedSchemaIssue(findSchemaIssue(verification.Issues, SchemaIssueColumnType, name), "updateColumn"),
					)
				}
				if nullMismatch {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues,
						blockedSchemaIssue(findSchemaIssue(verification.Issues, SchemaIssueColumnNullability, name), "updateColumn"),
					)
				}
				if autoIncrementMismatch {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues,
						blockedSchemaIssue(findSchemaIssue(verification.Issues, SchemaIssueColumnAutoIncrement, name), "updateColumn"),
					)
				}
				continue
			}
			statement, err := strategy.GenerateModifyColumnSQL(spec.tableName, field, name)
			if err != nil {
				return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
					"生成改列计划失败: table=%s, column=%s", spec.tableName, name,
				))
			}
			if err := appendSchemaMigrationAction(&tableReport, SchemaActionUpdateColumn, name, statement); err != nil {
				return nil, err
			}
		}

		actualIndexes, actualIndexNames := normalizedSchemaIndexes(actual.indexes)
		for _, key := range sortedSchemaIndexKeys(spec.indexes) {
			expected := spec.indexes[key]
			existing, exists := actualIndexes[key]
			if !exists {
				issue := findSchemaIssue(verification.Issues, SchemaIssueMissingIndex, expected.IndexName)
				if !permissions.CreateIndex {
					tableReport.BlockedIssues = append(tableReport.BlockedIssues,
						blockedSchemaIssue(issue, "createIndex"),
					)
					continue
				}
				statement, err := strategy.GenerateCreateIndexSQL(spec.tableName, expected)
				if err != nil {
					return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
						"生成索引计划失败: table=%s, index=%s", spec.tableName, expected.IndexName,
					))
				}
				if err := appendSchemaMigrationAction(&tableReport, SchemaActionCreateIndex, expected.IndexName, statement); err != nil {
					return nil, err
				}
				continue
			}
			if schemaIndexesEqual(existing, expected) {
				continue
			}
			issue := findSchemaIssue(verification.Issues, SchemaIssueIndexDefinition, expected.IndexName)
			if !permissions.ReplaceIndex {
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(issue, "replaceIndex"),
				)
				continue
			}
			dropStatement, err := strategy.GenerateDropIndexSQL(spec.tableName, expected.IndexName)
			if err != nil {
				return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
					"生成替换索引删除计划失败: table=%s, index=%s", spec.tableName, expected.IndexName,
				))
			}
			createStatement, err := strategy.GenerateCreateIndexSQL(spec.tableName, expected)
			if err != nil {
				return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
					"生成替换索引创建计划失败: table=%s, index=%s", spec.tableName, expected.IndexName,
				))
			}
			if err := appendSchemaMigrationAction(&tableReport, SchemaActionDeleteIndex, expected.IndexName, dropStatement); err != nil {
				return nil, err
			}
			if err := appendSchemaMigrationAction(&tableReport, SchemaActionCreateIndex, expected.IndexName, createStatement); err != nil {
				return nil, err
			}
		}

		extraIndexKeys := make([]string, 0, len(actualIndexes))
		for key := range actualIndexes {
			if _, expected := spec.indexes[key]; !expected {
				extraIndexKeys = append(extraIndexKeys, key)
			}
		}
		sort.Strings(extraIndexKeys)
		for _, key := range extraIndexKeys {
			name := actualIndexNames[key]
			issue := findSchemaIssue(verification.Issues, SchemaIssueExtraIndex, name)
			if !permissions.DeleteIndex {
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(issue, "deleteIndex"),
				)
				continue
			}
			statement, err := strategy.GenerateDropIndexSQL(spec.tableName, name)
			if err != nil {
				return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
					"生成删索引计划失败: table=%s, index=%s", spec.tableName, name,
				))
			}
			if err := appendSchemaMigrationAction(&tableReport, SchemaActionDeleteIndex, name, statement); err != nil {
				return nil, err
			}
		}

		expectedColumnSet := make(map[string]struct{}, len(spec.columns))
		for name := range spec.columns {
			expectedColumnSet[strings.ToLower(name)] = struct{}{}
		}
		extraColumnKeys := make([]string, 0, len(actualColumns))
		for key := range actualColumns {
			if _, expected := expectedColumnSet[key]; !expected {
				extraColumnKeys = append(extraColumnKeys, key)
			}
		}
		sort.Strings(extraColumnKeys)
		for _, key := range extraColumnKeys {
			name := actualColumnNames[key]
			issue := findSchemaIssue(verification.Issues, SchemaIssueExtraColumn, name)
			if !permissions.DeleteColumn {
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(issue, "deleteColumn"),
				)
				continue
			}
			if actualColumns[key].IsPrimary {
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(issue, "primaryKeyChange"),
				)
				continue
			}
			if dependency := schemaUndroppedIndexDependency(name, actualIndexes, spec.indexes, permissions); dependency != "" {
				issue.Actual = strings.TrimSpace(issue.Actual + ", index=" + dependency)
				tableReport.BlockedIssues = append(tableReport.BlockedIssues,
					blockedSchemaIssue(issue, "deleteColumnDependency"),
				)
				continue
			}
			statement, err := strategy.GenerateDropColumnSQL(spec.tableName, name)
			if err != nil {
				return nil, NewConfigurationExceptionWithCause(err, fmt.Sprintf(
					"生成删列计划失败: table=%s, column=%s", spec.tableName, name,
				))
			}
			if err := appendSchemaMigrationAction(&tableReport, SchemaActionDeleteColumn, name, statement); err != nil {
				return nil, err
			}
		}
		sortSchemaIssues(tableReport.BlockedIssues)
		reports = append(reports, tableReport)
	}
	return reports, nil
}

func appendSchemaMigrationAction(
	report *SchemaTableMigrationReport,
	kind SchemaActionKind,
	object string,
	statement string,
) error {
	if strings.TrimSpace(statement) == "" {
		return NewConfigurationException(fmt.Sprintf(
			"schema 策略生成了空 DDL: table=%s, action=%s, object=%s",
			report.TableName, kind, object,
		))
	}
	report.Actions = append(report.Actions, SchemaMigrationAction{
		Kind: kind, TableName: report.TableName, Object: object, Statement: statement,
	})
	return nil
}

func blockedSchemaIssue(issue SchemaIssue, permission string) SchemaIssue {
	issue.BlockedBy = permission
	return issue
}

func findSchemaIssue(issues []SchemaIssue, kind SchemaIssueKind, object string) SchemaIssue {
	for _, issue := range issues {
		if issue.Kind == kind && strings.EqualFold(issue.Object, object) {
			return issue
		}
	}
	return SchemaIssue{Kind: kind, Object: object}
}

func normalizedSchemaColumns(columns map[string]ColumnInfo) (map[string]ColumnInfo, map[string]string) {
	values := make(map[string]ColumnInfo, len(columns))
	names := make(map[string]string, len(columns))
	for name, column := range columns {
		key := strings.ToLower(name)
		values[key] = column
		names[key] = name
	}
	return values, names
}

func normalizedSchemaIndexes(indexes map[string]*IndexMetaData) (map[string]*IndexMetaData, map[string]string) {
	values := make(map[string]*IndexMetaData, len(indexes))
	names := make(map[string]string, len(indexes))
	for name, index := range indexes {
		key := strings.ToLower(name)
		values[key] = index
		names[key] = name
	}
	return values, names
}

func sortedSchemaIndexKeys(indexes map[string]*IndexMetaData) []string {
	keys := make([]string, 0, len(indexes))
	for key := range indexes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func schemaUndroppedIndexDependency(
	columnName string,
	actualIndexes map[string]*IndexMetaData,
	expectedIndexes map[string]*IndexMetaData,
	permissions SchemaMigrationPermissions,
) string {
	keys := make([]string, 0, len(actualIndexes))
	for key := range actualIndexes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		actual := actualIndexes[key]
		if !schemaIndexContainsColumn(actual, columnName) {
			continue
		}
		expected, declared := expectedIndexes[key]
		if !declared {
			if permissions.DeleteIndex {
				continue
			}
			return actual.IndexName
		}
		if !schemaIndexesEqual(actual, expected) && permissions.ReplaceIndex {
			continue
		}
		return actual.IndexName
	}
	return ""
}

func schemaIndexContainsColumn(index *IndexMetaData, columnName string) bool {
	if index == nil {
		return false
	}
	for _, candidate := range index.Columns {
		if strings.EqualFold(strings.TrimSpace(candidate), columnName) {
			return true
		}
	}
	return false
}

func executeSchemaMigrationPlan(
	ctx context.Context,
	db *Db,
	strategy IContextTableCreationStrategy,
	specs []schemaEntitySpec,
	reports []SchemaTableMigrationReport,
	maxConcurrency int,
) error {
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return NewQueryExceptionWithCause(ctxErr, "schema DDL 上下文已结束")
	}
	jobIndexes := make([]int, 0, len(reports))
	for index := range reports {
		if len(reports[index].Actions) > 0 {
			jobIndexes = append(jobIndexes, index)
		}
	}
	if len(jobIndexes) == 0 {
		return nil
	}
	workerCount := maxConcurrency
	if workerCount > len(jobIndexes) {
		workerCount = len(jobIndexes)
	}
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, len(jobIndexes))
	for _, index := range jobIndexes {
		jobs <- index
	}
	close(jobs)
	type executionResult struct {
		index int
		err   error
	}
	results := make(chan executionResult, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if jobCtx.Err() != nil {
					return
				}
				err := executeSchemaTablePlan(jobCtx, db, strategy, specs[index], &reports[index])
				results <- executionResult{index: index, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	var executionErrors []error
	for result := range results {
		if result.err != nil {
			executionErrors = append(executionErrors, fmt.Errorf(
				"执行表 %s schema 计划: %w", specs[result.index].tableName, result.err,
			))
		}
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		executionErrors = append(executionErrors, ctxErr)
	}
	return errors.Join(executionErrors...)
}

func executeSchemaTablePlan(
	ctx context.Context,
	db *Db,
	strategy IContextTableCreationStrategy,
	spec schemaEntitySpec,
	report *SchemaTableMigrationReport,
) error {
	for _, action := range report.Actions {
		if ctxErr := contextCauseError(ctx); ctxErr != nil {
			return NewQueryExceptionWithCause(ctxErr, fmt.Sprintf(
				"schema DDL 中断: table=%s, action=%s, object=%s",
				spec.tableName, action.Kind, action.Object,
			))
		}
		_, execErr := db.DataSource.ExecContext(ctx, action.Statement)
		if execErr == nil {
			report.Executed++
			continue
		}

		// MySQL 不提供事务化 DDL。并发实例可能刚好完成同一动作；只有重新读取
		// 后与该动作的目标状态等价，才可安全吞掉 duplicate/does-not-exist 错误。
		actual, observeErr := observeSchemaTable(ctx, db, strategy, spec.tableName)
		if observeErr == nil && schemaMigrationActionSatisfied(strategy, spec, action, actual) {
			report.Executed++
			continue
		}
		cause := joinErrorWithContext(execErr, ctx)
		if observeErr != nil {
			cause = errors.Join(cause, observeErr)
		}
		return NewQueryExceptionWithCause(cause, fmt.Sprintf(
			"schema DDL 失败: table=%s, action=%s, object=%s",
			spec.tableName, action.Kind, action.Object,
		))
	}
	return nil
}

func schemaMigrationActionSatisfied(
	strategy ITableCreationStrategy,
	spec schemaEntitySpec,
	action SchemaMigrationAction,
	actual schemaObservedTable,
) bool {
	columns, _ := normalizedSchemaColumns(actual.columns)
	indexes, _ := normalizedSchemaIndexes(actual.indexes)
	switch action.Kind {
	case SchemaActionCreateTable:
		if !actual.exists {
			return false
		}
		for name, field := range spec.columns {
			column, exists := columns[strings.ToLower(name)]
			if !exists || !schemaColumnMatches(strategy, spec, name, field, column, true) {
				return false
			}
		}
		return true
	case SchemaActionCreateColumn:
		field, exists := schemaSpecField(spec, action.Object)
		column, columnExists := columns[strings.ToLower(action.Object)]
		return exists && columnExists && schemaColumnMatches(strategy, spec, action.Object, field, column, true)
	case SchemaActionUpdateColumn:
		field, exists := schemaSpecField(spec, action.Object)
		column, columnExists := columns[strings.ToLower(action.Object)]
		return exists && columnExists && schemaColumnMatches(strategy, spec, action.Object, field, column, false)
	case SchemaActionDeleteColumn:
		_, exists := columns[strings.ToLower(action.Object)]
		return actual.exists && !exists
	case SchemaActionCreateIndex:
		expected := spec.indexes[strings.ToLower(action.Object)]
		existing, exists := indexes[strings.ToLower(action.Object)]
		return actual.exists && expected != nil && exists && schemaIndexesEqual(existing, expected)
	case SchemaActionDeleteIndex:
		existing, exists := indexes[strings.ToLower(action.Object)]
		if !actual.exists || !exists {
			return actual.exists
		}
		// 替换索引场景：并发方已经放置目标定义，也等价于删除动作已完成。
		expected := spec.indexes[strings.ToLower(action.Object)]
		return expected != nil && schemaIndexesEqual(existing, expected)
	default:
		return false
	}
}

func schemaSpecField(spec schemaEntitySpec, columnName string) (reflect.StructField, bool) {
	if field, exists := spec.columns[columnName]; exists {
		return field, true
	}
	for name, field := range spec.columns {
		if strings.EqualFold(name, columnName) {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func schemaColumnMatches(
	strategy ITableCreationStrategy,
	spec schemaEntitySpec,
	columnName string,
	field reflect.StructField,
	actual ColumnInfo,
	includePrimary bool,
) bool {
	if !schemaSQLTypesCompatible(actual.Type, strategy.GetSQLType(field)) ||
		actual.IsNullable != schemaExpectedNullable(spec, columnName, field) ||
		schemaColumnIsAutoIncrement(actual) != schemaExpectedAutoIncrement(field) {
		return false
	}
	return !includePrimary || actual.IsPrimary == schemaExpectedPrimary(spec, columnName, field)
}

func buildSchemaVerificationReport(
	strategy ITableCreationStrategy,
	specs []schemaEntitySpec,
	observed []schemaObservedTable,
) SchemaVerificationReport {
	report := SchemaVerificationReport{
		Compatible: true,
		Exact:      true,
		Tables:     make([]SchemaTableVerification, 0, len(specs)),
	}
	for index, spec := range specs {
		actual := schemaObservedTable{}
		if index < len(observed) {
			actual = observed[index]
		}
		table := verifySchemaTable(strategy, spec, actual)
		if !table.Compatible {
			report.Compatible = false
		}
		if !table.Exact {
			report.Exact = false
		}
		report.Tables = append(report.Tables, table)
	}
	return report
}

func verifySchemaTable(
	strategy ITableCreationStrategy,
	spec schemaEntitySpec,
	actual schemaObservedTable,
) SchemaTableVerification {
	table := SchemaTableVerification{
		TableName:  spec.tableName,
		EntityType: spec.entityName,
		Exists:     actual.exists,
		Compatible: true,
		Exact:      true,
	}
	if !actual.exists {
		table.Compatible = false
		table.Exact = false
		table.Issues = []SchemaIssue{{
			Kind:      SchemaIssueMissingTable,
			TableName: spec.tableName,
			Expected:  "present",
			Actual:    "missing",
		}}
		return table
	}

	actualColumns := make(map[string]ColumnInfo, len(actual.columns))
	actualColumnNames := make(map[string]string, len(actual.columns))
	for name, column := range actual.columns {
		key := strings.ToLower(name)
		actualColumns[key] = column
		actualColumnNames[key] = name
	}
	expectedColumnNames := make([]string, 0, len(spec.columns))
	expectedColumnSet := make(map[string]struct{}, len(spec.columns))
	for name := range spec.columns {
		expectedColumnNames = append(expectedColumnNames, name)
		expectedColumnSet[strings.ToLower(name)] = struct{}{}
	}
	sort.Strings(expectedColumnNames)
	for _, name := range expectedColumnNames {
		field := spec.columns[name]
		column, exists := actualColumns[strings.ToLower(name)]
		if !exists {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssueMissingColumn, TableName: spec.tableName, Object: name,
				Expected: schemaExpectedColumnSummary(strategy, spec, name, field), Actual: "missing",
			})
			continue
		}
		expectedType := strategy.GetSQLType(field)
		if !schemaSQLTypesCompatible(column.Type, expectedType) {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssueColumnType, TableName: spec.tableName, Object: name,
				Expected: normalizeSchemaSQLType(expectedType), Actual: normalizeSchemaSQLType(column.Type),
			})
		}
		expectedPrimary := schemaExpectedPrimary(spec, name, field)
		expectedNullable := schemaExpectedNullable(spec, name, field)
		if column.IsNullable != expectedNullable {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssueColumnNullability, TableName: spec.tableName, Object: name,
				Expected: fmt.Sprint(expectedNullable), Actual: fmt.Sprint(column.IsNullable),
			})
		}
		expectedAutoIncrement := schemaExpectedAutoIncrement(field)
		actualAutoIncrement := schemaColumnIsAutoIncrement(column)
		if actualAutoIncrement != expectedAutoIncrement {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssueColumnAutoIncrement, TableName: spec.tableName, Object: name,
				Expected: fmt.Sprint(expectedAutoIncrement), Actual: fmt.Sprint(actualAutoIncrement),
			})
		}
		if column.IsPrimary != expectedPrimary {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssuePrimaryKey, TableName: spec.tableName, Object: name,
				Expected: fmt.Sprint(expectedPrimary), Actual: fmt.Sprint(column.IsPrimary),
			})
		}
	}
	for key, name := range actualColumnNames {
		if _, exists := expectedColumnSet[key]; exists {
			continue
		}
		table.Exact = false
		table.Issues = append(table.Issues, SchemaIssue{
			Kind: SchemaIssueExtraColumn, TableName: spec.tableName, Object: name,
			Expected: "missing", Actual: "present",
		})
	}

	actualIndexes := make(map[string]*IndexMetaData, len(actual.indexes))
	actualIndexNames := make(map[string]string, len(actual.indexes))
	for name, index := range actual.indexes {
		key := strings.ToLower(name)
		actualIndexes[key] = index
		actualIndexNames[key] = name
	}
	expectedIndexKeys := make([]string, 0, len(spec.indexes))
	for key := range spec.indexes {
		expectedIndexKeys = append(expectedIndexKeys, key)
	}
	sort.Strings(expectedIndexKeys)
	for _, key := range expectedIndexKeys {
		expected := spec.indexes[key]
		index, exists := actualIndexes[key]
		if !exists {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssueMissingIndex, TableName: spec.tableName, Object: expected.IndexName,
				Expected: schemaIndexSummary(expected), Actual: "missing",
			})
			continue
		}
		if !schemaIndexesEqual(index, expected) {
			table.Compatible = false
			table.Exact = false
			table.Issues = append(table.Issues, SchemaIssue{
				Kind: SchemaIssueIndexDefinition, TableName: spec.tableName, Object: expected.IndexName,
				Expected: schemaIndexSummary(expected), Actual: schemaIndexSummary(index),
			})
		}
	}
	for key, name := range actualIndexNames {
		if _, exists := spec.indexes[key]; exists {
			continue
		}
		table.Exact = false
		table.Issues = append(table.Issues, SchemaIssue{
			Kind: SchemaIssueExtraIndex, TableName: spec.tableName, Object: name,
			Expected: "missing", Actual: schemaIndexSummary(actualIndexes[key]),
		})
	}
	sortSchemaIssues(table.Issues)
	return table
}

func schemaExpectedColumnSummary(
	strategy ITableCreationStrategy,
	spec schemaEntitySpec,
	name string,
	field reflect.StructField,
) string {
	return fmt.Sprintf(
		"type=%s,nullable=%t,primary=%t,autoIncrement=%t",
		normalizeSchemaSQLType(strategy.GetSQLType(field)),
		schemaExpectedNullable(spec, name, field),
		schemaExpectedPrimary(spec, name, field),
		schemaExpectedAutoIncrement(field),
	)
}

func schemaExpectedPrimary(spec schemaEntitySpec, columnName string, field reflect.StructField) bool {
	return GetCrudManagerInstance().IsPrimaryKey(field) || strings.EqualFold(columnName, spec.uidColumn)
}

func schemaExpectedNullable(spec schemaEntitySpec, columnName string, field reflect.StructField) bool {
	if schemaExpectedPrimary(spec, columnName, field) {
		return false
	}
	parts := strings.Split(field.Tag.Get("db"), ",")
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "not_null") {
			return false
		}
	}
	return true
}

func schemaExpectedAutoIncrement(field reflect.StructField) bool {
	return GetCrudManagerInstance().IsAutoIncrement(field)
}

func schemaColumnIsAutoIncrement(column ColumnInfo) bool {
	return column.IsAutoIncrement || strings.Contains(strings.ToLower(column.Extra), "auto_increment")
}

func schemaSQLTypesCompatible(actual, expected string) bool {
	return normalizeSchemaSQLType(actual) == normalizeSchemaSQLType(expected)
}

func normalizeSchemaSQLType(value string) string {
	value = strings.ToLower(strings.Join(strings.Fields(value), " "))
	value = strings.ReplaceAll(value, "integer", "int")
	value = strings.ReplaceAll(value, "double precision", "double")
	value = strings.ReplaceAll(value, "boolean", "tinyint(1)")
	value = schemaBoolPattern.ReplaceAllString(value, "tinyint(1)")
	value = schemaIntegerDisplayWidthPattern.ReplaceAllStringFunc(value, func(match string) string {
		if strings.EqualFold(match, "tinyint(1)") {
			return "tinyint(1)"
		}
		if index := strings.IndexByte(match, '('); index >= 0 {
			return match[:index]
		}
		return match
	})
	return value
}

func schemaIndexesEqual(actual, expected *IndexMetaData) bool {
	if actual == nil || expected == nil || actual.IsUnique != expected.IsUnique || len(actual.Columns) != len(expected.Columns) {
		return false
	}
	for index := range actual.Columns {
		if !strings.EqualFold(strings.TrimSpace(actual.Columns[index]), strings.TrimSpace(expected.Columns[index])) {
			return false
		}
	}
	return true
}

func schemaIndexSummary(index *IndexMetaData) string {
	if index == nil {
		return "<nil>"
	}
	kind := "index"
	if index.IsUnique {
		kind = "unique"
	}
	return kind + "(" + strings.Join(index.Columns, ",") + ")"
}

func sortSchemaIssues(issues []SchemaIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		left := string(issues[i].Kind) + "\x00" + issues[i].Object + "\x00" + issues[i].Expected + "\x00" + issues[i].Actual
		right := string(issues[j].Kind) + "\x00" + issues[j].Object + "\x00" + issues[j].Expected + "\x00" + issues[j].Actual
		return left < right
	})
}

func cloneSchemaVerificationReport(source SchemaVerificationReport) SchemaVerificationReport {
	result := source
	result.Tables = make([]SchemaTableVerification, len(source.Tables))
	for index, table := range source.Tables {
		result.Tables[index] = table
		result.Tables[index].Issues = append([]SchemaIssue(nil), table.Issues...)
	}
	return result
}

// TableExistsContext 是 MySQL metadata 的可取消版本。它不自行获取 Db generation
// 租约，由批量编排器在整个调用期间统一持有，避免 RWMutex 递归读锁死锁。
func (s *MySQLStrategy) TableExistsContext(
	ctx context.Context,
	db *Db,
	tableName string,
) (bool, error) {
	if err := validateMySQLSchemaContext(ctx, db, s); err != nil {
		return false, err
	}
	schemaName, unqualifiedTable, err := splitMySQLSchemaTable(tableName)
	if err != nil {
		return false, err
	}
	query, args := mysqlSchemaTableQuery(
		"SELECT COUNT(*) FROM information_schema.TABLES WHERE ",
		schemaName,
		unqualifiedTable,
	)
	var count int
	if err := db.DataSource.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, NewQueryExceptionWithCause(
			joinErrorWithContext(err, ctx),
			"检查 MySQL 表存在性失败",
		)
	}
	return count > 0, nil
}

// GetTableColumnsContext 读取完整 MySQL 列定义，并严格传播 query/scan/rows/close/context 错误。
func (s *MySQLStrategy) GetTableColumnsContext(
	ctx context.Context,
	db *Db,
	tableName string,
) (columns map[string]ColumnInfo, err error) {
	if validationErr := validateMySQLSchemaContext(ctx, db, s); validationErr != nil {
		return nil, validationErr
	}
	schemaName, unqualifiedTable, splitErr := splitMySQLSchemaTable(tableName)
	if splitErr != nil {
		return nil, splitErr
	}
	query, args := mysqlSchemaTableQuery(`
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA
		FROM information_schema.COLUMNS
		WHERE `, schemaName, unqualifiedTable)
	query += " ORDER BY ORDINAL_POSITION"
	rows, queryErr := db.DataSource.QueryContext(ctx, query, args...)
	if queryErr != nil {
		return nil, NewQueryExceptionWithCause(
			joinErrorWithContext(queryErr, ctx),
			"查询 MySQL 表列信息失败",
		)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			columns = nil
			err = errors.Join(err, NewQueryExceptionWithCause(
				joinErrorWithContext(closeErr, ctx),
				"关闭 MySQL 表列信息结果集失败",
			))
		}
	}()

	columns = make(map[string]ColumnInfo)
	for rows.Next() {
		var columnName, columnType, nullable, columnKey, extra string
		var defaultValue sql.NullString
		if scanErr := rows.Scan(&columnName, &columnType, &nullable, &columnKey, &defaultValue, &extra); scanErr != nil {
			return nil, NewQueryExceptionWithCause(
				joinErrorWithContext(scanErr, ctx),
				"扫描 MySQL 表列信息失败",
			)
		}
		column := ColumnInfo{
			Name:            columnName,
			Type:            columnType,
			IsNullable:      strings.EqualFold(nullable, "YES"),
			IsPrimary:       strings.EqualFold(columnKey, "PRI"),
			Extra:           extra,
			IsAutoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment"),
		}
		if defaultValue.Valid {
			column.Default = defaultValue.String
		}
		columns[columnName] = column
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, NewQueryExceptionWithCause(
			joinErrorWithContext(rowsErr, ctx),
			"遍历 MySQL 表列信息失败",
		)
	}
	return columns, nil
}

// GetExistingIndexesContext 按索引列逐行聚合，避免 GROUP_CONCAT 长度截断导致误判。
func (s *MySQLStrategy) GetExistingIndexesContext(
	ctx context.Context,
	db *Db,
	tableName string,
) (indexes map[string]*IndexMetaData, err error) {
	if validationErr := validateMySQLSchemaContext(ctx, db, s); validationErr != nil {
		return nil, validationErr
	}
	schemaName, unqualifiedTable, splitErr := splitMySQLSchemaTable(tableName)
	if splitErr != nil {
		return nil, splitErr
	}
	query, args := mysqlSchemaTableQuery(`
		SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE
		FROM information_schema.STATISTICS
		WHERE INDEX_NAME != 'PRIMARY' AND `, schemaName, unqualifiedTable)
	query += " ORDER BY INDEX_NAME, SEQ_IN_INDEX"
	rows, queryErr := db.DataSource.QueryContext(ctx, query, args...)
	if queryErr != nil {
		return nil, NewQueryExceptionWithCause(
			joinErrorWithContext(queryErr, ctx),
			"查询 MySQL 表索引信息失败",
		)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			indexes = nil
			err = errors.Join(err, NewQueryExceptionWithCause(
				joinErrorWithContext(closeErr, ctx),
				"关闭 MySQL 表索引信息结果集失败",
			))
		}
	}()

	indexes = make(map[string]*IndexMetaData)
	for rows.Next() {
		var indexName, columnName string
		var nonUnique int
		if scanErr := rows.Scan(&indexName, &columnName, &nonUnique); scanErr != nil {
			return nil, NewQueryExceptionWithCause(
				joinErrorWithContext(scanErr, ctx),
				"扫描 MySQL 表索引信息失败",
			)
		}
		unique := nonUnique == 0
		index := indexes[indexName]
		if index == nil {
			index = &IndexMetaData{IndexName: indexName, IsUnique: unique}
			indexes[indexName] = index
		} else if index.IsUnique != unique {
			return nil, NewQueryException(fmt.Sprintf(
				"MySQL 索引 metadata 不一致: table=%s, index=%s",
				tableName, indexName,
			))
		}
		index.Columns = append(index.Columns, columnName)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, NewQueryExceptionWithCause(
			joinErrorWithContext(rowsErr, ctx),
			"遍历 MySQL 表索引信息失败",
		)
	}
	return indexes, nil
}

func validateMySQLSchemaContext(ctx context.Context, db *Db, strategy *MySQLStrategy) error {
	if ctx == nil {
		return NewValidationException("context 不能为 nil")
	}
	if ctxErr := contextCauseError(ctx); ctxErr != nil {
		return NewQueryExceptionWithCause(ctxErr, "MySQL schema metadata 上下文已结束")
	}
	if db == nil || db.DataSource == nil {
		return NewQueryException("数据库连接未初始化")
	}
	if strategy == nil || strategy.cm == nil {
		return NewConfigurationException("MySQL 建表策略未初始化")
	}
	return nil
}

func splitMySQLSchemaTable(tableName string) (string, string, error) {
	if err := validateRepositoryTableIdentifier(tableName); err != nil {
		return "", "", err
	}
	parts := strings.Split(tableName, ".")
	switch len(parts) {
	case 1:
		return "", parts[0], nil
	case 2:
		return parts[0], parts[1], nil
	default:
		return "", "", NewValidationException(fmt.Sprintf(
			"MySQL schema 表名最多包含两段: %s", safeValueForLog(tableName),
		))
	}
}

func mysqlSchemaTableQuery(prefix, schemaName, tableName string) (string, []any) {
	if schemaName == "" {
		return prefix + "TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", []any{tableName}
	}
	return prefix + "TABLE_SCHEMA = ? AND TABLE_NAME = ?", []any{schemaName, tableName}
}
