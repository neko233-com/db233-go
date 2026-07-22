package db233

import (
	"context"
	"database/sql/driver"
	"testing"
)

const ormMappingBenchmarkRows = 32

func BenchmarkOrmMappingTarget_Legacy(b *testing.B) {
	benchmarkOrmMappingTarget(b, false, false)
}

func BenchmarkOrmMappingTarget_Fast(b *testing.B) {
	benchmarkOrmMappingTarget(b, true, false)
}

func BenchmarkOrmMappingTarget_Strict(b *testing.B) {
	benchmarkOrmMappingTarget(b, true, true)
}

func BenchmarkOrmMappingTarget_StrictLegacy(b *testing.B) {
	benchmarkOrmMappingTarget(b, false, true)
}

func benchmarkOrmMappingTarget(b *testing.B, fast, strict bool) {
	b.Helper()
	applyStrictTestSettings(b, fast, false, 0)

	rows := make([][]driver.Value, ormMappingBenchmarkRows)
	for i := range rows {
		rows[i] = []driver.Value{
			"benchmark-id",
			int64(i),
			"note",
			[]byte(`{"wins":3,"losses":1}`),
			"forward-compatible",
		}
	}
	state := newScriptedDBState()
	state.suppressCalls = true
	state.repeatQuery = &scriptedStep{
		kind:    "query",
		columns: []string{"id", "score", "note", "payload", "future_column"},
		rows:    rows,
	}
	db := newStrictTestDb(b, state)
	ctx := context.Background()
	query := "SELECT * FROM strict_contract_entity"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if strict {
			mapped, err := db.ExecuteQueryStrictContext(ctx, query, nil, &strictContractEntity{})
			if err != nil {
				b.Fatalf("strict mapping: %v", err)
			}
			if len(mapped) != ormMappingBenchmarkRows {
				b.Fatalf("strict mapped rows=%d, want %d", len(mapped), ormMappingBenchmarkRows)
			}
			continue
		}

		resultRows, err := db.DataSource.QueryContext(ctx, query)
		if err != nil {
			b.Fatalf("query rows: %v", err)
		}
		mapped := OrmHandlerInstance.OrmBatch(resultRows, &strictContractEntity{})
		if len(mapped) != ormMappingBenchmarkRows {
			b.Fatalf("mapped rows=%d, want %d", len(mapped), ormMappingBenchmarkRows)
		}
	}
}
