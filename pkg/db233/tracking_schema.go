package db233

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// TrackingSchema 是埋点表结构描述文件的根对象。
// 它独立于 IDbEntity/CrudManager 自动建表机制，可与现有 entity 自动建表同时使用。
type TrackingSchema struct {
	Version string          `json:"version"`
	Tables  []TrackingTable `json:"tables"`
}

// TrackingTable 描述一张埋点表。
type TrackingTable struct {
	Name    string           `json:"name"`
	Comment string           `json:"comment,omitempty"`
	Columns []TrackingColumn `json:"columns"`
	Indexes []TrackingIndex  `json:"indexes,omitempty"`
}

// TrackingColumn 描述埋点列和上报 KV 约束。
type TrackingColumn struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	SQLType       string   `json:"sqlType,omitempty"`
	Size          int      `json:"size,omitempty"`
	Required      bool     `json:"required,omitempty"`
	PrimaryKey    bool     `json:"primaryKey,omitempty"`
	AutoIncrement bool     `json:"autoIncrement,omitempty"`
	Nullable      *bool    `json:"nullable,omitempty"`
	Default       any      `json:"default,omitempty"`
	Enum          []string `json:"enum,omitempty"`
	Comment       string   `json:"comment,omitempty"`
}

// TrackingIndex 描述埋点表索引。
type TrackingIndex struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

// TrackingSchemaApplyOptions 控制描述文件建表/迁移行为。
type TrackingSchemaApplyOptions struct {
	Permission        *AutoDbPermission
	DryRun            bool
	StrictTypeCheck   bool
	AllowUnknownTypes bool
	CachePath         string
	DisableLocalCache bool
	ForceApply        bool
}

// TrackingSchemaPlan 是一次描述文件同步的计划。
type TrackingSchemaPlan struct {
	Hash       string
	Changed    bool
	Statements []TrackingSchemaStatement
	Warnings   []string
}

// TrackingSchemaStatement 是一条待执行或已规划 SQL。
type TrackingSchemaStatement struct {
	Table     string
	SQL       string
	Operation string
}

// TrackingSchemaLocalCache 是埋点描述文件同步的本地记录。
// 用于重启后判断描述文件是否变化，避免文件未改时重复做结构检查。
type TrackingSchemaLocalCache struct {
	SchemaPath     string    `json:"schemaPath"`
	SchemaHash     string    `json:"schemaHash"`
	SchemaVersion  string    `json:"schemaVersion,omitempty"`
	AppliedAt      time.Time `json:"appliedAt"`
	StatementCount int       `json:"statementCount"`
}

// TrackingPayloadError 表示上报 KV 不符合描述文件。
type TrackingPayloadError struct {
	Field   string
	Message string
}

func (e TrackingPayloadError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// TrackingSchemaReloader 轮询描述文件变更，变更时校验并同步表结构。
type TrackingSchemaReloader struct {
	db       *Db
	path     string
	interval time.Duration
	options  TrackingSchemaApplyOptions
	onReload func(*TrackingSchema, TrackingSchemaPlan)
	onError  func(error)

	mu       sync.RWMutex
	current  *TrackingSchema
	lastHash string
	running  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
}

var trackingIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadTrackingSchemaFile 加载 JSONC 埋点描述文件（.json，支持 // 与 /* */ 注释）。
func LoadTrackingSchemaFile(path string) (*TrackingSchema, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", NewConfigurationExceptionWithCause(err, "读取埋点描述文件失败")
	}
	hashBytes := sha256.Sum256(raw)
	hash := hex.EncodeToString(hashBytes[:])

	var schema TrackingSchema
	cleaned, err := stripJSONComments(raw)
	if err != nil {
		return nil, "", NewConfigurationExceptionWithCause(err, "解析埋点描述文件注释失败")
	}
	if err := json.Unmarshal(cleaned, &schema); err != nil {
		return nil, "", NewConfigurationExceptionWithCause(err, "解析埋点描述文件失败")
	}
	if err := schema.Validate(); err != nil {
		return nil, "", err
	}
	return &schema, hash, nil
}

// Validate 校验描述文件结构、名称、类型、索引引用。
func (s *TrackingSchema) Validate() error {
	if s == nil {
		return NewValidationException("埋点描述不能为空")
	}
	if len(s.Tables) == 0 {
		return NewValidationException("埋点描述至少需要一张表")
	}
	tableNames := make(map[string]bool, len(s.Tables))
	for _, table := range s.Tables {
		if err := validateTrackingIdentifier("表名", table.Name); err != nil {
			return err
		}
		if tableNames[table.Name] {
			return NewValidationException("重复表名: " + table.Name)
		}
		tableNames[table.Name] = true
		if len(table.Columns) == 0 {
			return NewValidationException("表 " + table.Name + " 至少需要一列")
		}
		columnNames := make(map[string]bool, len(table.Columns))
		primaryKeys := 0
		for _, column := range table.Columns {
			if err := validateTrackingIdentifier("列名", column.Name); err != nil {
				return err
			}
			if columnNames[column.Name] {
				return NewValidationException("表 " + table.Name + " 存在重复列: " + column.Name)
			}
			columnNames[column.Name] = true
			if column.PrimaryKey {
				primaryKeys++
			}
			if column.Type == "" && column.SQLType == "" {
				return NewValidationException("列 " + table.Name + "." + column.Name + " 必须声明 type 或 sqlType")
			}
			if column.Type != "" {
				if _, err := trackingColumnSQLType(column); err != nil {
					return err
				}
			}
			if column.AutoIncrement && !isTrackingIntegerType(column.Type) {
				return NewValidationException("自增列必须使用 int/int64 类型: " + table.Name + "." + column.Name)
			}
		}
		if primaryKeys == 0 {
			return NewValidationException("表 " + table.Name + " 必须至少声明一个 primaryKey 列")
		}
		for _, index := range table.Indexes {
			if err := validateTrackingIdentifier("索引名", index.Name); err != nil {
				return err
			}
			if len(index.Columns) == 0 {
				return NewValidationException("索引 " + table.Name + "." + index.Name + " 必须声明 columns")
			}
			for _, col := range index.Columns {
				if !columnNames[col] {
					return NewValidationException("索引 " + table.Name + "." + index.Name + " 引用不存在列: " + col)
				}
			}
		}
	}
	return nil
}

// GetTable 获取表描述。
func (s *TrackingSchema) GetTable(name string) (*TrackingTable, bool) {
	if s == nil {
		return nil, false
	}
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i], true
		}
	}
	return nil, false
}

// GetColumn 获取列描述。
func (t *TrackingTable) GetColumn(name string) (*TrackingColumn, bool) {
	if t == nil {
		return nil, false
	}
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i], true
		}
	}
	return nil, false
}

// PayloadColumns 返回上报 KV 应包含的列。主键列默认也保留，调用方可自行剔除 playerId。
func (t *TrackingTable) PayloadColumns() []TrackingColumn {
	if t == nil {
		return nil
	}
	result := make([]TrackingColumn, 0, len(t.Columns))
	for _, col := range t.Columns {
		if col.AutoIncrement {
			continue
		}
		result = append(result, col)
	}
	return result
}

// ValidatePayload 校验客户端上报 KV 是否符合表描述。
// allowUnknown=false 时，未知字段直接报错；适合严格 DSL 上报。
func (t *TrackingTable) ValidatePayload(payload map[string]any, allowUnknown bool) []error {
	if t == nil {
		return []error{TrackingPayloadError{Message: "表描述为空"}}
	}
	columns := make(map[string]TrackingColumn, len(t.Columns))
	for _, col := range t.PayloadColumns() {
		columns[col.Name] = col
	}
	var errs []error
	for _, col := range columns {
		if col.Required {
			if _, ok := payload[col.Name]; !ok {
				errs = append(errs, TrackingPayloadError{Field: col.Name, Message: "缺少必填字段"})
			}
		}
	}
	for key, value := range payload {
		col, ok := columns[key]
		if !ok {
			if !allowUnknown {
				errs = append(errs, TrackingPayloadError{Field: key, Message: "未知字段"})
			}
			continue
		}
		if value == nil {
			if col.Required || !trackingColumnNullable(col) {
				errs = append(errs, TrackingPayloadError{Field: key, Message: "不允许为空"})
			}
			continue
		}
		if err := validateTrackingPayloadValue(col, value); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// PlanTrackingSchema 根据当前数据库结构生成同步计划。
func PlanTrackingSchema(db *Db, schema *TrackingSchema, options *TrackingSchemaApplyOptions) (TrackingSchemaPlan, error) {
	if db == nil || db.DataSource == nil {
		return TrackingSchemaPlan{}, NewConfigurationException("db 不能为空")
	}
	if schema == nil {
		return TrackingSchemaPlan{}, NewValidationException("埋点描述不能为空")
	}
	if err := schema.Validate(); err != nil {
		return TrackingSchemaPlan{}, err
	}
	opts := normalizeTrackingApplyOptions(options)
	if db.DatabaseType != "" && db.DatabaseType != EnumDatabaseTypeMySQL {
		return TrackingSchemaPlan{}, NewDb233Exception("埋点描述自动建表当前仅支持 MySQL")
	}
	hash, err := trackingSchemaHash(schema)
	if err != nil {
		return TrackingSchemaPlan{}, err
	}

	strategy := GetStrategyFactoryInstance().GetStrategy(EnumDatabaseTypeMySQL)
	plan := TrackingSchemaPlan{Hash: hash}
	for _, table := range schema.Tables {
		exists, err := strategy.TableExists(db, table.Name)
		if err != nil {
			return plan, err
		}
		if !exists {
			if opts.Permission.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
				createSQL, err := generateTrackingCreateTableSQL(table)
				if err != nil {
					return plan, err
				}
				plan.Statements = append(plan.Statements, TrackingSchemaStatement{
					Table:     table.Name,
					SQL:       createSQL,
					Operation: "create_table",
				})
				for _, idx := range table.Indexes {
					sqlStr, err := generateTrackingCreateIndexSQL(table.Name, idx)
					if err != nil {
						return plan, err
					}
					plan.Statements = append(plan.Statements, TrackingSchemaStatement{
						Table:     table.Name,
						SQL:       sqlStr,
						Operation: "create_index",
					})
				}
			}
			continue
		}

		existingColumns, err := strategy.GetTableColumns(db, table.Name)
		if err != nil {
			return plan, err
		}
		for _, col := range table.Columns {
			expectedType, err := trackingColumnSQLType(col)
			if err != nil {
				return plan, err
			}
			existing, ok := existingColumns[col.Name]
			if !ok {
				if opts.Permission.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
					sqlStr, err := generateTrackingAddColumnSQL(table.Name, col)
					if err != nil {
						return plan, err
					}
					plan.Statements = append(plan.Statements, TrackingSchemaStatement{
						Table:     table.Name,
						SQL:       sqlStr,
						Operation: "add_column",
					})
				}
				continue
			}
			if opts.StrictTypeCheck && !trackingSQLTypeCompatible(existing.Type, expectedType) {
				if opts.Permission.IsAllowed(EnumAutoDbOperateTypeUpdateColumn) {
					sqlStr, err := generateTrackingModifyColumnSQL(table.Name, col)
					if err != nil {
						return plan, err
					}
					plan.Statements = append(plan.Statements, TrackingSchemaStatement{
						Table:     table.Name,
						SQL:       sqlStr,
						Operation: "modify_column",
					})
				} else {
					plan.Warnings = append(plan.Warnings,
						fmt.Sprintf("列类型不一致但未允许修改: %s.%s db=%s schema=%s", table.Name, col.Name, existing.Type, expectedType))
				}
			}
		}

		existingIndexes, err := strategy.GetExistingIndexes(db, table.Name)
		if err != nil {
			return plan, err
		}
		for _, idx := range table.Indexes {
			expected := &IndexMetaData{IndexName: idx.Name, Columns: idx.Columns, IsUnique: idx.Unique}
			existing, ok := existingIndexes[idx.Name]
			if ok && indexEqual(existing, expected) && existing.IsUnique == expected.IsUnique {
				continue
			}
			if ok {
				if !opts.Permission.IsAllowed(EnumAutoDbOperateTypeUpdateColumn) {
					plan.Warnings = append(plan.Warnings, "索引不一致但未允许修改: "+table.Name+"."+idx.Name)
					continue
				}
				dropSQL, err := strategy.GenerateDropIndexSQL(table.Name, idx.Name)
				if err != nil {
					return plan, err
				}
				plan.Statements = append(plan.Statements, TrackingSchemaStatement{
					Table:     table.Name,
					SQL:       dropSQL,
					Operation: "drop_index",
				})
			}
			if opts.Permission.IsAllowed(EnumAutoDbOperateTypeCreateColumn) {
				createSQL, err := generateTrackingCreateIndexSQL(table.Name, idx)
				if err != nil {
					return plan, err
				}
				plan.Statements = append(plan.Statements, TrackingSchemaStatement{
					Table:     table.Name,
					SQL:       createSQL,
					Operation: "create_index",
				})
			}
		}
	}
	plan.Changed = len(plan.Statements) > 0 || len(plan.Warnings) > 0
	return plan, nil
}

// ApplyTrackingSchema 执行描述文件同步。默认只创建表、补列、建索引，不删列。
func ApplyTrackingSchema(db *Db, schema *TrackingSchema, options *TrackingSchemaApplyOptions) (TrackingSchemaPlan, error) {
	if db == nil || db.DataSource == nil {
		return TrackingSchemaPlan{}, NewConfigurationException("db 不能为空")
	}
	_, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return TrackingSchemaPlan{}, generationErr
	}
	defer releaseGeneration()
	plan, err := PlanTrackingSchema(db, schema, options)
	if err != nil {
		return plan, err
	}
	opts := normalizeTrackingApplyOptions(options)
	if opts.DryRun {
		return plan, nil
	}
	for _, stmt := range plan.Statements {
		if _, err := db.DataSource.Exec(stmt.SQL); err != nil {
			return plan, NewQueryExceptionWithCause(err, "埋点表结构同步失败: "+sqlForError(stmt.SQL))
		}
	}
	return plan, nil
}

// ApplyTrackingSchemaFile 加载文件并同步表结构。
func ApplyTrackingSchemaFile(db *Db, path string, options *TrackingSchemaApplyOptions) (*TrackingSchema, TrackingSchemaPlan, error) {
	schema, hash, err := LoadTrackingSchemaFile(path)
	if err != nil {
		return nil, TrackingSchemaPlan{}, err
	}
	opts := normalizeTrackingApplyOptions(options)
	cachePath := resolveTrackingSchemaCachePath(path, opts)
	if !opts.DisableLocalCache && !opts.ForceApply && !opts.DryRun {
		cache, err := LoadTrackingSchemaLocalCache(cachePath)
		if err == nil && cache.SchemaHash == hash && sameTrackingSchemaPath(cache.SchemaPath, path) {
			return schema, TrackingSchemaPlan{Hash: hash, Changed: false}, nil
		}
	}
	plan, err := ApplyTrackingSchema(db, schema, options)
	if err != nil {
		return schema, plan, err
	}
	plan.Hash = hash
	if !opts.DisableLocalCache && !opts.DryRun {
		cache := &TrackingSchemaLocalCache{
			SchemaPath:     path,
			SchemaHash:     hash,
			SchemaVersion:  schema.Version,
			AppliedAt:      time.Now(),
			StatementCount: len(plan.Statements),
		}
		if err := SaveTrackingSchemaLocalCache(cachePath, cache); err != nil {
			return schema, plan, err
		}
	}
	return schema, plan, nil
}

// LoadTrackingSchemaLocalCache 读取埋点描述同步本地记录。
func LoadTrackingSchemaLocalCache(path string) (*TrackingSchemaLocalCache, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache TrackingSchemaLocalCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

// SaveTrackingSchemaLocalCache 写入埋点描述同步本地记录。
func SaveTrackingSchemaLocalCache(path string, cache *TrackingSchemaLocalCache) error {
	if cache == nil {
		return NewValidationException("埋点描述本地记录不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return NewConfigurationExceptionWithCause(err, "创建埋点描述本地记录目录失败")
	}
	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return NewConfigurationExceptionWithCause(err, "序列化埋点描述本地记录失败")
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return NewConfigurationExceptionWithCause(err, "写入埋点描述本地记录失败")
	}
	return nil
}

// NewTrackingSchemaReloader 创建描述文件热重载器。
func NewTrackingSchemaReloader(db *Db, path string, interval time.Duration, options TrackingSchemaApplyOptions) *TrackingSchemaReloader {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &TrackingSchemaReloader{
		db:       db,
		path:     path,
		interval: interval,
		options:  options,
	}
}

// OnReload 设置热重载成功回调。
func (r *TrackingSchemaReloader) OnReload(fn func(*TrackingSchema, TrackingSchemaPlan)) *TrackingSchemaReloader {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onReload = fn
	return r
}

// OnError 设置热重载失败回调。
func (r *TrackingSchemaReloader) OnError(fn func(error)) *TrackingSchemaReloader {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onError = fn
	return r
}

// Start 启动热重载轮询。
func (r *TrackingSchemaReloader) Start() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	r.running = true
	r.stopCh = stopCh
	r.doneCh = doneCh
	r.mu.Unlock()
	go r.loop(stopCh, doneCh)
}

// Stop 停止热重载轮询。
func (r *TrackingSchemaReloader) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	doneCh := r.doneCh
	if r.stopCh != nil {
		close(r.stopCh)
		r.stopCh = nil
	}
	r.mu.Unlock()

	<-doneCh
	r.mu.Lock()
	if r.doneCh == doneCh {
		r.running = false
		r.doneCh = nil
	}
	r.mu.Unlock()
}

// Current 返回当前已加载描述。
func (r *TrackingSchemaReloader) Current() *TrackingSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneTrackingSchema(r.current)
}

func (r *TrackingSchemaReloader) loop(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)
	r.reloadOnce()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.reloadOnce()
		case <-stopCh:
			return
		}
	}
}

func (r *TrackingSchemaReloader) reloadOnce() {
	schema, hash, err := LoadTrackingSchemaFile(r.path)
	if err != nil {
		r.emitError(err)
		return
	}
	opts := normalizeTrackingApplyOptions(&r.options)
	cachePath := resolveTrackingSchemaCachePath(r.path, opts)
	r.mu.RLock()
	unchanged := hash == r.lastHash
	r.mu.RUnlock()
	if unchanged && !opts.ForceApply {
		return
	}
	if !opts.DisableLocalCache && !opts.ForceApply && !opts.DryRun {
		cache, err := LoadTrackingSchemaLocalCache(cachePath)
		if err == nil && cache.SchemaHash == hash && sameTrackingSchemaPath(cache.SchemaPath, r.path) {
			r.mu.Lock()
			r.current = cloneTrackingSchema(schema)
			r.lastHash = hash
			r.mu.Unlock()
			return
		}
	}
	plan, err := ApplyTrackingSchema(r.db, schema, &r.options)
	if err != nil {
		r.emitError(err)
		return
	}
	plan.Hash = hash
	if !opts.DisableLocalCache && !opts.DryRun {
		cache := &TrackingSchemaLocalCache{
			SchemaPath:     r.path,
			SchemaHash:     hash,
			SchemaVersion:  schema.Version,
			AppliedAt:      time.Now(),
			StatementCount: len(plan.Statements),
		}
		if err := SaveTrackingSchemaLocalCache(cachePath, cache); err != nil {
			r.emitError(err)
			return
		}
	}
	r.mu.Lock()
	r.current = cloneTrackingSchema(schema)
	r.lastHash = hash
	onReload := r.onReload
	r.mu.Unlock()
	if onReload != nil {
		onReload(cloneTrackingSchema(schema), cloneTrackingSchemaPlan(plan))
	}
}

func (r *TrackingSchemaReloader) emitError(err error) {
	r.mu.RLock()
	onError := r.onError
	r.mu.RUnlock()
	if onError != nil {
		onError(err)
		return
	}
	LogError("埋点描述热重载失败: %s", safeErrorForLog(err))
}

func cloneTrackingSchema(schema *TrackingSchema) *TrackingSchema {
	if schema == nil {
		return nil
	}
	clone := &TrackingSchema{Version: schema.Version, Tables: make([]TrackingTable, len(schema.Tables))}
	for i, table := range schema.Tables {
		clone.Tables[i] = table
		clone.Tables[i].Columns = make([]TrackingColumn, len(table.Columns))
		for columnIndex, column := range table.Columns {
			clone.Tables[i].Columns[columnIndex] = column
			clone.Tables[i].Columns[columnIndex].Enum = append([]string(nil), column.Enum...)
			clone.Tables[i].Columns[columnIndex].Default = cloneTrackingValue(column.Default)
			if column.Nullable != nil {
				nullable := *column.Nullable
				clone.Tables[i].Columns[columnIndex].Nullable = &nullable
			}
		}
		clone.Tables[i].Indexes = make([]TrackingIndex, len(table.Indexes))
		for index, item := range table.Indexes {
			clone.Tables[i].Indexes[index] = item
			clone.Tables[i].Indexes[index].Columns = append([]string(nil), item.Columns...)
		}
	}
	return clone
}

func cloneTrackingSchemaPlan(plan TrackingSchemaPlan) TrackingSchemaPlan {
	plan.Statements = append([]TrackingSchemaStatement(nil), plan.Statements...)
	plan.Warnings = append([]string(nil), plan.Warnings...)
	return plan
}

func cloneTrackingValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(item))
		for key, nested := range item {
			clone[key] = cloneTrackingValue(nested)
		}
		return clone
	case []any:
		clone := make([]any, len(item))
		for index, nested := range item {
			clone[index] = cloneTrackingValue(nested)
		}
		return clone
	case []string:
		return append([]string(nil), item...)
	default:
		return item
	}
}

func normalizeTrackingApplyOptions(options *TrackingSchemaApplyOptions) TrackingSchemaApplyOptions {
	opts := TrackingSchemaApplyOptions{}
	if options != nil {
		opts = *options
	}
	if opts.Permission == nil {
		opts.Permission = NewSafeAutoDbPermission()
	}
	return opts
}

func resolveTrackingSchemaCachePath(schemaPath string, opts TrackingSchemaApplyOptions) string {
	if opts.CachePath != "" {
		return opts.CachePath
	}
	return schemaPath + ".cache.json"
}

func sameTrackingSchemaPath(a string, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return strings.EqualFold(absA, absB)
	}
	return strings.EqualFold(a, b)
}

func validateTrackingIdentifier(kind string, name string) error {
	if name == "" {
		return NewValidationException(kind + "不能为空")
	}
	if !trackingIdentifierPattern.MatchString(name) {
		return NewValidationException(kind + "非法: " + name)
	}
	return nil
}

func stripJSONComments(raw []byte) ([]byte, error) {
	result := make([]byte, 0, len(raw))
	inString := false
	escaped := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inLineComment {
			if ch == '\n' || ch == '\r' {
				inLineComment = false
				result = append(result, ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(raw) && raw[i+1] == '/' {
				inBlockComment = false
				i++
				continue
			}
			if ch == '\n' || ch == '\r' {
				result = append(result, ch)
			} else {
				result = append(result, ' ')
			}
			continue
		}
		if inString {
			result = append(result, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			result = append(result, ch)
			continue
		}
		if ch == '/' && i+1 < len(raw) {
			next := raw[i+1]
			if next == '/' {
				inLineComment = true
				i++
				continue
			}
			if next == '*' {
				inBlockComment = true
				i++
				continue
			}
		}
		result = append(result, ch)
	}
	if inBlockComment {
		return nil, fmt.Errorf("未闭合的块注释")
	}
	return result, nil
}

func trackingColumnSQLType(col TrackingColumn) (string, error) {
	if col.SQLType != "" {
		return strings.ToUpper(strings.TrimSpace(col.SQLType)), nil
	}
	switch strings.ToLower(col.Type) {
	case "string":
		size := col.Size
		if size <= 0 {
			size = 255
		}
		return fmt.Sprintf("VARCHAR(%d)", size), nil
	case "text":
		return "TEXT", nil
	case "int", "int32":
		return "INT", nil
	case "int64", "long":
		return "BIGINT", nil
	case "float", "float32":
		return "FLOAT", nil
	case "double", "float64":
		return "DOUBLE", nil
	case "bool", "boolean":
		return "TINYINT(1)", nil
	case "time", "datetime", "timestamp":
		return "TIMESTAMP", nil
	case "json", "object", "map", "array":
		return "MEDIUMTEXT", nil
	default:
		return "", NewValidationException("不支持的埋点字段类型: " + col.Type)
	}
}

func trackingColumnNullable(col TrackingColumn) bool {
	if col.PrimaryKey || col.Required {
		return false
	}
	if col.Nullable != nil {
		return *col.Nullable
	}
	return true
}

func isTrackingIntegerType(t string) bool {
	switch strings.ToLower(t) {
	case "int", "int32", "int64", "long":
		return true
	default:
		return false
	}
}

func generateTrackingCreateTableSQL(table TrackingTable) (string, error) {
	var defs []string
	var primaryKeys []string
	for _, col := range table.Columns {
		def, err := generateTrackingColumnDefinition(col, true)
		if err != nil {
			return "", err
		}
		defs = append(defs, def)
		if col.PrimaryKey {
			primaryKeys = append(primaryKeys, quoteTrackingIdentifier(col.Name))
		}
	}
	if len(primaryKeys) > 0 {
		defs = append(defs, "PRIMARY KEY ("+strings.Join(primaryKeys, ", ")+")")
	}
	sqlStr := fmt.Sprintf("CREATE TABLE `%s` (\n\t%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
		table.Name, strings.Join(defs, ",\n\t"))
	if table.Comment != "" {
		sqlStr += " COMMENT=" + quoteTrackingString(table.Comment)
	}
	return sqlStr, nil
}

func generateTrackingAddColumnSQL(tableName string, col TrackingColumn) (string, error) {
	def, err := generateTrackingColumnDefinition(col, false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s", tableName, def), nil
}

func generateTrackingModifyColumnSQL(tableName string, col TrackingColumn) (string, error) {
	def, err := generateTrackingColumnDefinition(col, false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN %s", tableName, def), nil
}

func generateTrackingColumnDefinition(col TrackingColumn, creatingTable bool) (string, error) {
	sqlType, err := trackingColumnSQLType(col)
	if err != nil {
		return "", err
	}
	def := fmt.Sprintf("`%s` %s", col.Name, sqlType)
	if col.AutoIncrement {
		def += " AUTO_INCREMENT"
	}
	if trackingColumnNullable(col) {
		def += " NULL"
	} else {
		def += " NOT NULL"
	}
	if col.Default != nil && !col.AutoIncrement {
		def += " DEFAULT " + formatTrackingDefault(col.Default)
	}
	if col.Comment != "" {
		def += " COMMENT " + quoteTrackingString(col.Comment)
	}
	if col.PrimaryKey && !creatingTable {
		def += " PRIMARY KEY"
	}
	return def, nil
}

func generateTrackingCreateIndexSQL(tableName string, idx TrackingIndex) (string, error) {
	if err := validateTrackingIdentifier("索引名", idx.Name); err != nil {
		return "", err
	}
	if len(idx.Columns) == 0 {
		return "", NewValidationException("索引必须声明 columns: " + idx.Name)
	}
	quotedColumns := make([]string, len(idx.Columns))
	for i, col := range idx.Columns {
		if err := validateTrackingIdentifier("索引列名", col); err != nil {
			return "", err
		}
		quotedColumns[i] = quoteTrackingIdentifier(col)
	}
	indexType := "INDEX"
	if idx.Unique {
		indexType = "UNIQUE INDEX"
	}
	return fmt.Sprintf("CREATE %s `%s` ON `%s` (%s)", indexType, idx.Name, tableName, strings.Join(quotedColumns, ", ")), nil
}

func quoteTrackingIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func quoteTrackingString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func formatTrackingDefault(value any) string {
	switch v := value.(type) {
	case string:
		upper := strings.ToUpper(v)
		if upper == "CURRENT_TIMESTAMP" || upper == "NULL" {
			return upper
		}
		return quoteTrackingString(v)
	case bool:
		if v {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(v)
	}
}

func trackingSQLTypeCompatible(actual string, expected string) bool {
	return normalizeTrackingSQLType(actual) == normalizeTrackingSQLType(expected)
}

func normalizeTrackingSQLType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.Join(strings.Fields(v), " ")
	return v
}

func trackingSchemaHash(schema *TrackingSchema) (string, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	hashBytes := sha256.Sum256(raw)
	return hex.EncodeToString(hashBytes[:]), nil
}

func validateTrackingPayloadValue(col TrackingColumn, value any) error {
	switch strings.ToLower(col.Type) {
	case "string", "text":
		str, ok := value.(string)
		if !ok {
			return TrackingPayloadError{Field: col.Name, Message: "类型应为 string"}
		}
		if col.Size > 0 && len(str) > col.Size {
			return TrackingPayloadError{Field: col.Name, Message: fmt.Sprintf("长度超过 %d", col.Size)}
		}
		if len(col.Enum) > 0 && !trackingStringIn(col.Enum, str) {
			return TrackingPayloadError{Field: col.Name, Message: "值不在 enum 范围"}
		}
	case "int", "int32", "int64", "long":
		if !isTrackingNumeric(value, false) {
			return TrackingPayloadError{Field: col.Name, Message: "类型应为整数"}
		}
	case "float", "float32", "double", "float64":
		if !isTrackingNumeric(value, true) {
			return TrackingPayloadError{Field: col.Name, Message: "类型应为数字"}
		}
	case "bool", "boolean":
		if _, ok := value.(bool); !ok {
			return TrackingPayloadError{Field: col.Name, Message: "类型应为 bool"}
		}
	case "time", "datetime", "timestamp":
		switch v := value.(type) {
		case time.Time:
			return nil
		case string:
			if _, err := time.Parse(time.RFC3339, v); err == nil {
				return nil
			}
			if _, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
				return nil
			}
			return TrackingPayloadError{Field: col.Name, Message: "时间格式应为 RFC3339 或 2006-01-02 15:04:05"}
		default:
			return TrackingPayloadError{Field: col.Name, Message: "类型应为 time/string"}
		}
	case "json", "object", "map", "array":
		return nil
	default:
		if col.SQLType != "" {
			return nil
		}
		return TrackingPayloadError{Field: col.Name, Message: "未知 DSL 类型"}
	}
	return nil
}

func isTrackingNumeric(value any, allowFloat bool) bool {
	if value == nil {
		return false
	}
	if number, ok := value.(json.Number); ok {
		if allowFloat {
			_, err := number.Float64()
			return err == nil
		}
		_, err := number.Int64()
		return err == nil
	}
	t := reflect.TypeOf(value)
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		if allowFloat {
			return true
		}
		return reflect.ValueOf(value).Float() == float64(int64(reflect.ValueOf(value).Float()))
	default:
		return false
	}
}

func trackingStringIn(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// BuildTrackingInsertSQL 生成描述表对应的 INSERT SQL 和参数。
// payload 可是客户端 JSON map；函数按描述文件列顺序取值。
func (t *TrackingTable) BuildTrackingInsertSQL(payload map[string]any) (string, []any, error) {
	if t == nil {
		return "", nil, NewValidationException("表描述为空")
	}
	if errs := t.ValidatePayload(payload, false); len(errs) > 0 {
		return "", nil, errs[0]
	}
	columns := make([]string, 0, len(t.Columns))
	placeholders := make([]string, 0, len(t.Columns))
	values := make([]any, 0, len(t.Columns))
	for _, col := range t.Columns {
		if col.AutoIncrement {
			continue
		}
		value, ok := payload[col.Name]
		if !ok {
			continue
		}
		columns = append(columns, quoteTrackingIdentifier(col.Name))
		placeholders = append(placeholders, "?")
		values = append(values, value)
	}
	if len(columns) == 0 {
		return "", nil, NewValidationException("没有可插入字段")
	}
	sqlStr := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", t.Name, strings.Join(columns, ", "), strings.Join(placeholders, ", "))
	return sqlStr, values, nil
}

// InsertTrackingPayload 校验并写入一条埋点上报。
func InsertTrackingPayload(db *Db, table *TrackingTable, payload map[string]any) (sql.Result, error) {
	if db == nil || db.DataSource == nil {
		return nil, NewConfigurationException("db 不能为空")
	}
	_, releaseGeneration, generationErr := db.lockCurrentDatabaseGeneration()
	if generationErr != nil {
		return nil, generationErr
	}
	defer releaseGeneration()
	sqlStr, values, err := table.BuildTrackingInsertSQL(payload)
	if err != nil {
		return nil, err
	}
	result, execErr := db.DataSource.Exec(sqlStr, values...)
	if execErr != nil {
		return nil, NewQueryExceptionWithCause(execErr, "埋点写入失败: "+sqlForError(sqlStr))
	}
	return result, nil
}

// TrackingSchemaTables 返回稳定排序后的表名，便于上层检查当前已加载 DSL。
func (s *TrackingSchema) TrackingSchemaTables() []string {
	if s == nil {
		return nil
	}
	names := make([]string, 0, len(s.Tables))
	for _, table := range s.Tables {
		names = append(names, table.Name)
	}
	sort.Strings(names)
	return names
}
