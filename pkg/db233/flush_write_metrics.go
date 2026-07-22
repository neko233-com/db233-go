package db233

import (
	"sync/atomic"
	"time"
)

// FlushWriteSource identifies the pipeline that issued a state-flush SQL.
// It never contains table names, SQL text, parameters, player IDs, or errors.
type FlushWriteSource string

const (
	// FlushWriteSourceManual is an explicit UpdateBatchUpsert call made outside
	// the Session and WriteBuffer pipelines.
	FlushWriteSourceManual FlushWriteSource = "manual"
	// FlushWriteSourceSession includes periodic, explicit, logout, shutdown, and
	// database-generation Session flushes.
	FlushWriteSourceSession FlushWriteSource = "session"
	// FlushWriteSourceWriteBuffer includes automatic and explicit WriteBuffer flushes.
	FlushWriteSourceWriteBuffer FlushWriteSource = "write_buffer"
	// FlushWriteSourceWALReplay is a LocalWriteJournal recovery replay.
	FlushWriteSourceWALReplay FlushWriteSource = "wal_replay"
	// FlushWriteSourceFaultToleranceReplay is a FaultTolerantManager recovery replay.
	FlushWriteSourceFaultToleranceReplay FlushWriteSource = "fault_tolerance_replay"
	// FlushWriteSourceOther is reserved for internal flush pipelines that do not
	// match another stable source.
	FlushWriteSourceOther FlushWriteSource = "other"
)

var flushWriteSources = [...]FlushWriteSource{
	FlushWriteSourceManual,
	FlushWriteSourceSession,
	FlushWriteSourceWriteBuffer,
	FlushWriteSourceWALReplay,
	FlushWriteSourceFaultToleranceReplay,
	FlushWriteSourceOther,
}

// FlushWriteCounters are monotonic counters for actual state-flush SQL calls.
// One merged SQL statement, including each chunk, counts as one SQL regardless
// of how many entities it contains.
type FlushWriteCounters struct {
	AttemptedSQL      uint64 `json:"attemptedSql"`
	SucceededSQL      uint64 `json:"succeededSql"`
	FailedSQL         uint64 `json:"failedSql"`
	AttemptedEntities uint64 `json:"attemptedEntities"`
	SucceededEntities uint64 `json:"succeededEntities"`
	FailedEntities    uint64 `json:"failedEntities"`
}

// FlushWriteMetricsSnapshot is a lock-free point-in-time snapshot. Counters
// start at the Db's first actual flush SQL attempt and remain monotonic for that
// Db's lifetime. Concurrent writes may become visible across adjacent snapshots.
type FlushWriteMetricsSnapshot struct {
	FlushWriteCounters

	StartedAt time.Time     `json:"startedAt"`
	SampledAt time.Time     `json:"sampledAt"`
	Elapsed   time.Duration `json:"elapsed"`

	AverageAttemptedSQLPerSecond      float64 `json:"averageAttemptedSqlPerSecond"`
	AverageSucceededSQLPerSecond      float64 `json:"averageSucceededSqlPerSecond"`
	AverageFailedSQLPerSecond         float64 `json:"averageFailedSqlPerSecond"`
	AverageAttemptedEntitiesPerSecond float64 `json:"averageAttemptedEntitiesPerSecond"`
	AverageSucceededEntitiesPerSecond float64 `json:"averageSucceededEntitiesPerSecond"`
	AverageFailedEntitiesPerSecond    float64 `json:"averageFailedEntitiesPerSecond"`

	BySource map[FlushWriteSource]FlushWriteCounters `json:"bySource"`
}

// FlushWriteRates describes rates between two caller-selected snapshots.
// This lets callers use any operational window without a hot-path timer or ring buffer.
type FlushWriteRates struct {
	Window time.Duration `json:"window"`

	AttemptedSQLPerSecond      float64 `json:"attemptedSqlPerSecond"`
	SucceededSQLPerSecond      float64 `json:"succeededSqlPerSecond"`
	FailedSQLPerSecond         float64 `json:"failedSqlPerSecond"`
	AttemptedEntitiesPerSecond float64 `json:"attemptedEntitiesPerSecond"`
	SucceededEntitiesPerSecond float64 `json:"succeededEntitiesPerSecond"`
	FailedEntitiesPerSecond    float64 `json:"failedEntitiesPerSecond"`

	BySource map[FlushWriteSource]FlushWriteSourceRates `json:"bySource"`
}

// FlushWriteSourceRates contains per-source rates for a caller-selected window.
type FlushWriteSourceRates struct {
	AttemptedSQLPerSecond      float64 `json:"attemptedSqlPerSecond"`
	SucceededSQLPerSecond      float64 `json:"succeededSqlPerSecond"`
	FailedSQLPerSecond         float64 `json:"failedSqlPerSecond"`
	AttemptedEntitiesPerSecond float64 `json:"attemptedEntitiesPerSecond"`
	SucceededEntitiesPerSecond float64 `json:"succeededEntitiesPerSecond"`
	FailedEntitiesPerSecond    float64 `json:"failedEntitiesPerSecond"`
}

type atomicFlushWriteCounters struct {
	attemptedSQL      atomic.Uint64
	succeededSQL      atomic.Uint64
	failedSQL         atomic.Uint64
	attemptedEntities atomic.Uint64
	succeededEntities atomic.Uint64
	failedEntities    atomic.Uint64
}

type flushWriteMetricsCollector struct {
	startedUnixNano atomic.Int64
	bySource        [len(flushWriteSources)]atomicFlushWriteCounters
}

func flushWriteSourceIndex(source FlushWriteSource) int {
	switch source {
	case FlushWriteSourceManual:
		return 0
	case FlushWriteSourceSession:
		return 1
	case FlushWriteSourceWriteBuffer:
		return 2
	case FlushWriteSourceWALReplay:
		return 3
	case FlushWriteSourceFaultToleranceReplay:
		return 4
	default:
		return 5
	}
}

func (collector *flushWriteMetricsCollector) recordAttempt(source FlushWriteSource, entityCount int) {
	if collector == nil {
		return
	}
	if collector.startedUnixNano.Load() == 0 {
		collector.startedUnixNano.CompareAndSwap(0, time.Now().UnixNano())
	}
	counters := &collector.bySource[flushWriteSourceIndex(source)]
	counters.attemptedSQL.Add(1)
	if entityCount > 0 {
		counters.attemptedEntities.Add(uint64(entityCount))
	}
}

func (collector *flushWriteMetricsCollector) recordResult(source FlushWriteSource, entityCount int, succeeded bool) {
	if collector == nil {
		return
	}
	counters := &collector.bySource[flushWriteSourceIndex(source)]
	if succeeded {
		counters.succeededSQL.Add(1)
		if entityCount > 0 {
			counters.succeededEntities.Add(uint64(entityCount))
		}
		return
	}
	counters.failedSQL.Add(1)
	if entityCount > 0 {
		counters.failedEntities.Add(uint64(entityCount))
	}
}

func (collector *flushWriteMetricsCollector) snapshot(sampledAt time.Time) FlushWriteMetricsSnapshot {
	snapshot := FlushWriteMetricsSnapshot{
		SampledAt: sampledAt,
		BySource:  make(map[FlushWriteSource]FlushWriteCounters, len(flushWriteSources)),
	}
	if collector == nil {
		for _, source := range flushWriteSources {
			snapshot.BySource[source] = FlushWriteCounters{}
		}
		return snapshot
	}

	for index, source := range flushWriteSources {
		counters := collector.bySource[index].snapshot()
		snapshot.BySource[source] = counters
		snapshot.FlushWriteCounters.add(counters)
	}
	startedUnixNano := collector.startedUnixNano.Load()
	if startedUnixNano == 0 {
		return snapshot
	}
	snapshot.StartedAt = time.Unix(0, startedUnixNano)
	snapshot.Elapsed = sampledAt.Sub(snapshot.StartedAt)
	if snapshot.Elapsed <= 0 {
		snapshot.Elapsed = 0
		return snapshot
	}
	snapshot.setLifetimeRates()
	return snapshot
}

func (counters *atomicFlushWriteCounters) snapshot() FlushWriteCounters {
	if counters == nil {
		return FlushWriteCounters{}
	}
	return FlushWriteCounters{
		AttemptedSQL:      counters.attemptedSQL.Load(),
		SucceededSQL:      counters.succeededSQL.Load(),
		FailedSQL:         counters.failedSQL.Load(),
		AttemptedEntities: counters.attemptedEntities.Load(),
		SucceededEntities: counters.succeededEntities.Load(),
		FailedEntities:    counters.failedEntities.Load(),
	}
}

func (counters *FlushWriteCounters) add(other FlushWriteCounters) {
	counters.AttemptedSQL += other.AttemptedSQL
	counters.SucceededSQL += other.SucceededSQL
	counters.FailedSQL += other.FailedSQL
	counters.AttemptedEntities += other.AttemptedEntities
	counters.SucceededEntities += other.SucceededEntities
	counters.FailedEntities += other.FailedEntities
}

func (snapshot *FlushWriteMetricsSnapshot) setLifetimeRates() {
	snapshot.AverageAttemptedSQLPerSecond = flushRate(snapshot.AttemptedSQL, snapshot.Elapsed)
	snapshot.AverageSucceededSQLPerSecond = flushRate(snapshot.SucceededSQL, snapshot.Elapsed)
	snapshot.AverageFailedSQLPerSecond = flushRate(snapshot.FailedSQL, snapshot.Elapsed)
	snapshot.AverageAttemptedEntitiesPerSecond = flushRate(snapshot.AttemptedEntities, snapshot.Elapsed)
	snapshot.AverageSucceededEntitiesPerSecond = flushRate(snapshot.SucceededEntities, snapshot.Elapsed)
	snapshot.AverageFailedEntitiesPerSecond = flushRate(snapshot.FailedEntities, snapshot.Elapsed)
}

func flushRate(count uint64, elapsed time.Duration) float64 {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(count) / seconds
}

// FlushWriteMetrics returns cumulative state-flush SQL pressure for this Db.
// AttemptedSQL is the actual database/sql execution-attempt count, so failed
// calls also contribute to pressure. SQL preparation, WAL append, serialization,
// and SQL construction failures before Exec are not counted.
func (db *Db) FlushWriteMetrics() FlushWriteMetricsSnapshot {
	sampledAt := time.Now()
	if db == nil {
		return (*flushWriteMetricsCollector)(nil).snapshot(sampledAt)
	}
	return db.flushWriteMetrics.snapshot(sampledAt)
}

// AverageFlushWritesPerSecond returns the lifetime average number of actual
// state-flush database/sql Exec attempts per second since the first attempt.
func (db *Db) AverageFlushWritesPerSecond() float64 {
	return db.FlushWriteMetrics().AverageAttemptedSQLPerSecond
}

func (db *Db) recordFlushWriteAttempt(source FlushWriteSource, entityCount int) {
	if db == nil {
		return
	}
	db.flushWriteMetrics.recordAttempt(source, entityCount)
}

func (db *Db) recordFlushWriteResult(source FlushWriteSource, entityCount int, succeeded bool) {
	if db == nil {
		return
	}
	db.flushWriteMetrics.recordResult(source, entityCount, succeeded)
}

// RateSince calculates rates over the caller-selected interval between two
// snapshots from the same Db. Non-positive windows return zero rates.
func (snapshot FlushWriteMetricsSnapshot) RateSince(previous FlushWriteMetricsSnapshot) FlushWriteRates {
	window := snapshot.SampledAt.Sub(previous.SampledAt)
	rates := FlushWriteRates{
		Window:   window,
		BySource: make(map[FlushWriteSource]FlushWriteSourceRates, len(flushWriteSources)),
	}
	if window <= 0 {
		rates.Window = 0
		for _, source := range flushWriteSources {
			rates.BySource[source] = FlushWriteSourceRates{}
		}
		return rates
	}

	delta := snapshot.FlushWriteCounters.subtract(previous.FlushWriteCounters)
	rates.AttemptedSQLPerSecond = flushRate(delta.AttemptedSQL, window)
	rates.SucceededSQLPerSecond = flushRate(delta.SucceededSQL, window)
	rates.FailedSQLPerSecond = flushRate(delta.FailedSQL, window)
	rates.AttemptedEntitiesPerSecond = flushRate(delta.AttemptedEntities, window)
	rates.SucceededEntitiesPerSecond = flushRate(delta.SucceededEntities, window)
	rates.FailedEntitiesPerSecond = flushRate(delta.FailedEntities, window)
	for _, source := range flushWriteSources {
		sourceDelta := snapshot.BySource[source].subtract(previous.BySource[source])
		rates.BySource[source] = FlushWriteSourceRates{
			AttemptedSQLPerSecond:      flushRate(sourceDelta.AttemptedSQL, window),
			SucceededSQLPerSecond:      flushRate(sourceDelta.SucceededSQL, window),
			FailedSQLPerSecond:         flushRate(sourceDelta.FailedSQL, window),
			AttemptedEntitiesPerSecond: flushRate(sourceDelta.AttemptedEntities, window),
			SucceededEntitiesPerSecond: flushRate(sourceDelta.SucceededEntities, window),
			FailedEntitiesPerSecond:    flushRate(sourceDelta.FailedEntities, window),
		}
	}
	return rates
}

func (counters FlushWriteCounters) subtract(previous FlushWriteCounters) FlushWriteCounters {
	return FlushWriteCounters{
		AttemptedSQL:      monotonicDelta(counters.AttemptedSQL, previous.AttemptedSQL),
		SucceededSQL:      monotonicDelta(counters.SucceededSQL, previous.SucceededSQL),
		FailedSQL:         monotonicDelta(counters.FailedSQL, previous.FailedSQL),
		AttemptedEntities: monotonicDelta(counters.AttemptedEntities, previous.AttemptedEntities),
		SucceededEntities: monotonicDelta(counters.SucceededEntities, previous.SucceededEntities),
		FailedEntities:    monotonicDelta(counters.FailedEntities, previous.FailedEntities),
	}
}

func monotonicDelta(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}
