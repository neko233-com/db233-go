package db233

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

type runtimeCacheEntity struct{}

func (*runtimeCacheEntity) TableName() string       { return "runtime_cache_entity" }
func (*runtimeCacheEntity) SerializeBeforeSaveDb()  {}
func (*runtimeCacheEntity) DeserializeAfterLoadDb() {}

type runtimeCacheEntityB struct{}

func (*runtimeCacheEntityB) TableName() string       { return "runtime_cache_entity_b" }
func (*runtimeCacheEntityB) SerializeBeforeSaveDb()  {}
func (*runtimeCacheEntityB) DeserializeAfterLoadDb() {}

func TestRuntimeComponents_SQLTemplateCacheKeyAndBound(t *testing.T) {
	cache := newSqlTemplateCache(32)
	entity := &runtimeCacheEntity{}

	tests := []struct {
		table string
		uid   string
		want  string
	}{
		{table: "tenant_a", uid: "id", want: "SELECT * FROM tenant_a WHERE id = ?"},
		{table: "tenant_b", uid: "id", want: "SELECT * FROM tenant_b WHERE id = ?"},
		{table: "tenant_a", uid: "account_id", want: "SELECT * FROM tenant_a WHERE account_id = ?"},
	}
	for _, test := range tests {
		if got := cache.GetFindByIdSQL(entity, test.table, test.uid); got != test.want {
			t.Fatalf("缓存键串用: got=%q want=%q", got, test.want)
		}
	}
	cache.GetFindByIdSQL(&runtimeCacheEntityB{}, "tenant_a", "id")
	cache.mu.RLock()
	if got := len(cache.findById); got != len(tests)+1 {
		cache.mu.RUnlock()
		t.Fatalf("缓存键未包含实体类型: got=%d want=%d", got, len(tests)+1)
	}
	cache.mu.RUnlock()

	const workers = 24
	const iterations = 100
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				table := fmt.Sprintf("tenant_%d_%d", worker, iteration)
				uid := fmt.Sprintf("uid_%d", iteration%3)
				want := "SELECT * FROM " + table + " WHERE " + uid + " = ?"
				if got := cache.GetFindByIdSQL(entity, table, uid); got != want {
					errs <- fmt.Errorf("并发缓存串用: got=%q want=%q", got, want)
					return
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if got := len(cache.findById); got > cache.capacity {
		t.Fatalf("动态表缓存无界: size=%d capacity=%d", got, cache.capacity)
	}
	if got := len(cache.insertionKeys); got > cache.capacity {
		t.Fatalf("淘汰队列无界: size=%d capacity=%d", got, cache.capacity)
	}
}

func TestRuntimeComponents_LoggerLevelIsAtomicAndValidated(t *testing.T) {
	logger := newLogger(INFO, log.New(io.Discard, "", 0))
	logger.SetLevel(DEBUG)
	logger.SetLevel(LogLevel(-1))
	logger.SetLevel(FATAL + 1)
	if got := logger.GetLevel(); got != DEBUG {
		t.Fatalf("非法等级不应覆盖当前等级: got=%v want=%v", got, DEBUG)
	}

	levels := []LogLevel{TRACE, DEBUG, INFO, WARN, ERROR, FATAL}
	const workers = 32
	const iterations = 200
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				logger.SetLevel(levels[(worker+iteration)%len(levels)])
				logger.Debug("worker=%d iteration=%d", worker, iteration)
			}
		}(worker)
	}
	wg.Wait()
	if got := logger.GetLevel(); !got.valid() {
		t.Fatalf("并发设置后得到非法等级: %v", got)
	}
}

func TestRuntimeComponents_ExecuteSQLContextDefensiveCopiesAndConcurrency(t *testing.T) {
	originalParams := []any{"original"}
	context := NewExecuteSqlContext("SELECT 1", originalParams)
	originalParams[0] = "mutated"
	if got := context.GetParams()[0]; got != "original" {
		t.Fatalf("构造函数保留了 Params 别名: %v", got)
	}

	paramsSnapshot := context.GetParams()
	paramsSnapshot[0] = "snapshot-mutated"
	if got := context.GetParams()[0]; got != "original" {
		t.Fatalf("GetParams 暴露内部 slice: %v", got)
	}

	replacementParams := []any{"replacement"}
	context.SetParams(replacementParams)
	replacementParams[0] = "mutated"
	if got := context.GetParams()[0]; got != "replacement" {
		t.Fatalf("SetParams 保留了输入 slice 别名: %v", got)
	}

	originalAttributes := map[string]any{"role": "reader"}
	context.SetAttributes(originalAttributes)
	originalAttributes["role"] = "writer"
	if got := context.GetAttribute("role"); got != "reader" {
		t.Fatalf("SetAttributes 保留了输入 map 别名: %v", got)
	}
	attributesSnapshot := context.GetAttributes()
	attributesSnapshot["role"] = "admin"
	if got := context.GetAttribute("role"); got != "reader" {
		t.Fatalf("GetAttributes 暴露内部 map: %v", got)
	}

	const workers = 32
	const iterations = 100
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			key := fmt.Sprintf("worker-%d", worker)
			for iteration := 0; iteration < iterations; iteration++ {
				context.SetAttribute(key, iteration)
				_ = context.GetAttribute(key)
				context.SetParams([]any{worker, iteration})
				_ = context.GetParams()
				_ = context.ParamCount()
			}
		}(worker)
	}
	wg.Wait()
	if got := len(context.GetAttributes()); got != workers+1 {
		t.Fatalf("并发属性写入丢失: got=%d want=%d", got, workers+1)
	}
}

func TestRuntimeComponents_BuiltinPluginsAreSafeAndRedactParams(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	loggingPlugin := NewLoggingPlugin()
	literalSecret := "literal-production-secret"
	parameterSecret := "parameter-production-secret"
	secretSQL := "SELECT * FROM users WHERE token = '" + literalSecret + "' AND password = ?"
	secretContext := NewExecuteSqlContext(secretSQL, []any{map[string]string{"password": parameterSecret}})
	loggingPlugin.PreExecuteSql(secretContext)
	logged := output.String()
	if strings.Contains(logged, literalSecret) || strings.Contains(logged, parameterSecret) {
		t.Fatalf("SQL 字面量或参数泄漏到默认日志: %q", logged)
	}
	if !strings.Contains(logged, "SQLVerb: SELECT") || strings.Contains(logged, "SQLHash:") ||
		strings.Contains(logged, "SQLLength:") || strings.Contains(logged, "ParamCount:") {
		t.Fatalf("默认日志缺少安全 SQL 元数据: %q", logged)
	}

	output.Reset()
	performancePlugin := NewPerformanceMonitorPlugin(time.Nanosecond)
	secretContext.Duration = time.Millisecond
	performancePlugin.PostExecuteSql(secretContext)
	slowQueryLog := output.String()
	if strings.Contains(slowQueryLog, literalSecret) || strings.Contains(slowQueryLog, parameterSecret) {
		t.Fatalf("慢查询日志泄漏 SQL 字面量或参数: %q", slowQueryLog)
	}
	if !strings.Contains(slowQueryLog, "SQLVerb: SELECT") || strings.Contains(slowQueryLog, "SQLHash:") || strings.Contains(slowQueryLog, "SQLLength:") {
		t.Fatalf("慢查询日志缺少安全 SQL 元数据: %q", slowQueryLog)
	}

	output.Reset()
	loggingPlugin.SetLogFullSQL(true)
	loggingPlugin.PreExecuteSql(secretContext)
	optInLog := output.String()
	if !strings.Contains(optInLog, literalSecret) {
		t.Fatalf("显式开启后应记录完整 SQL: %q", optInLog)
	}
	if strings.Contains(optInLog, parameterSecret) {
		t.Fatalf("即使开启完整 SQL 也不得记录参数值: %q", optInLog)
	}

	metricsPlugin := NewMetricsPlugin()
	context := NewExecuteSqlContext("SELECT 1", nil)
	context.Duration = 2 * time.Millisecond
	context.Error = errors.New("query failed")
	const workers = 32
	const iterations = 200
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				metricsPlugin.PostExecuteSql(context)
				if iteration%10 == 0 {
					_ = metricsPlugin.GetMetrics()
				}
			}
		}()
	}
	wg.Wait()

	wantQueries := workers * iterations
	metrics := metricsPlugin.GetMetrics()
	if got := metrics["total_queries"].(int); got != wantQueries {
		t.Fatalf("查询指标丢失: got=%d want=%d", got, wantQueries)
	}
	if got := metrics["error_count"].(int); got != wantQueries {
		t.Fatalf("错误指标丢失: got=%d want=%d", got, wantQueries)
	}
	wantDuration := time.Duration(wantQueries) * context.Duration
	if got := metrics["total_duration"].(time.Duration); got != wantDuration {
		t.Fatalf("耗时指标丢失: got=%v want=%v", got, wantDuration)
	}

	metrics["total_queries"] = -1
	if got := metricsPlugin.GetMetrics()["total_queries"].(int); got != wantQueries {
		t.Fatalf("GetMetrics 暴露内部状态: got=%d want=%d", got, wantQueries)
	}
}
