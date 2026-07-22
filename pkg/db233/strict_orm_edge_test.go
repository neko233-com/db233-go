package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
)

type strictSmallInt int8
type strictText string

type strictScannerDTO struct {
	Name    sql.NullString         `db:"name"`
	Count   *sql.NullInt64         `db:"count"`
	Payload strictRetainingScanner `db:"payload"`
	Failure strictFailingScanner   `db:"failure"`
}

type strictRetainingScanner struct {
	Value []byte
}

func (s *strictRetainingScanner) Scan(source any) error {
	value, ok := source.([]byte)
	if !ok {
		return fmt.Errorf("strict retaining scanner: unexpected %T", source)
	}
	// Deliberately retain source. Strict ORM must pass owned bytes.
	s.Value = value
	return nil
}

var errStrictScanner = errors.New("strict scanner failed")

type strictFailingScanner struct{}

func (*strictFailingScanner) Scan(any) error { return errStrictScanner }

type strictByteOwnershipDTO struct {
	Value any `db:"value"`
}

type StrictIgnoredEmbeddedForTest struct {
	Value string `db:"value"`
}

type strictIgnoredEmbeddedDTO struct {
	*StrictIgnoredEmbeddedForTest `db:"-"`
}

func TestStrictNumericConversionsRejectLoss(t *testing.T) {
	tests := []struct {
		name       string
		source     driver.Value
		targetType reflect.Type
	}{
		{name: "int64 overflows int8", source: int64(128), targetType: reflect.TypeOf(int8(0))},
		{name: "bytes overflow int8", source: []byte("128"), targetType: reflect.TypeOf(int8(0))},
		{name: "negative signed to uint", source: int64(-1), targetType: reflect.TypeOf(uint8(0))},
		{name: "fractional float to int", source: float64(1.5), targetType: reflect.TypeOf(int64(0))},
		{name: "empty numeric text", source: []byte{}, targetType: reflect.TypeOf(int64(0))},
		{name: "float64 overflows float32", source: math.MaxFloat64, targetType: reflect.TypeOf(float32(0))},
		{name: "int64 loses float64 precision", source: int64(1<<53 + 1), targetType: reflect.TypeOf(float64(0))},
		{name: "float64 loses float32 precision", source: float64(0.1), targetType: reflect.TypeOf(float32(0))},
		{name: "float text overflow", source: []byte("1e400"), targetType: reflect.TypeOf(float64(0))},
		{name: "float text NaN", source: []byte("NaN"), targetType: reflect.TypeOf(float64(0))},
		{name: "float text positive infinity", source: []byte("+Inf"), targetType: reflect.TypeOf(float64(0))},
		{name: "float text negative infinity", source: []byte("-Inf"), targetType: reflect.TypeOf(float32(0))},
	}

	for _, fast := range []bool{false, true} {
		mode := "legacy"
		if fast {
			mode = "fast"
		}
		for _, test := range tests {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				_, err := strictSingleFieldQuery(t, fast, test.source, test.targetType)
				if err == nil {
					t.Fatalf("strict conversion unexpectedly succeeded: source=%T(%v), target=%s", test.source, test.source, test.targetType)
				}
			})
		}
	}
}

func TestStrictNumericConversionsPreserveNamedTypesAndText(t *testing.T) {
	tests := []struct {
		name       string
		source     driver.Value
		targetType reflect.Type
		want       any
	}{
		{name: "named int", source: int64(127), targetType: reflect.TypeOf(strictSmallInt(0)), want: strictSmallInt(127)},
		{name: "numeric to named text", source: int64(65), targetType: reflect.TypeOf(strictText("")), want: strictText("65")},
		{name: "integral float to int", source: float64(42), targetType: reflect.TypeOf(int64(0)), want: int64(42)},
		{name: "exact large int to float", source: int64(1 << 53), targetType: reflect.TypeOf(float64(0)), want: float64(1 << 53)},
		{name: "float32 widened by driver", source: float64(float32(0.1)), targetType: reflect.TypeOf(float32(0)), want: float32(0.1)},
		{name: "normal decimal text to float64", source: []byte("0.1"), targetType: reflect.TypeOf(float64(0)), want: float64(0.1)},
		{name: "normal decimal text to float32", source: []byte("1.25"), targetType: reflect.TypeOf(float32(0)), want: float32(1.25)},
		{name: "exact decimal text to float64", source: []byte("0.5"), targetType: reflect.TypeOf(float64(0)), want: float64(0.5)},
		{name: "exact integer text to float32", source: []byte("16777216"), targetType: reflect.TypeOf(float32(0)), want: float32(16777216)},
		{name: "rounded integer text to float32", source: []byte("16777217"), targetType: reflect.TypeOf(float32(0)), want: float32(16777217)},
	}

	for _, fast := range []bool{false, true} {
		mode := "legacy"
		if fast {
			mode = "fast"
		}
		for _, test := range tests {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				got, err := strictSingleFieldQuery(t, fast, test.source, test.targetType)
				if err != nil {
					t.Fatalf("strict conversion failed: %v", err)
				}
				if !reflect.DeepEqual(got.Interface(), test.want) {
					t.Fatalf("strict conversion=%#v, want %#v", got.Interface(), test.want)
				}
			})
		}
	}
}

func TestStrictScannerAndByteOwnership(t *testing.T) {
	for _, fast := range []bool{false, true} {
		mode := "legacy"
		if fast {
			mode = "fast"
		}
		t.Run(mode, func(t *testing.T) {
			applyStrictTestSettings(t, fast, false, 0)
			rawPayload := []byte("scanner-owned")
			state := newScriptedDBState(scriptedStep{
				kind:    "query",
				columns: []string{"name", "count", "payload"},
				rows:    [][]driver.Value{{[]byte("alice"), int64(7), rawPayload}},
			})
			db := newStrictTestDb(t, state)
			rows, err := db.ExecuteQueryStrictContext(context.Background(), "SELECT scanner fields", nil, &strictScannerDTO{})
			if err != nil {
				t.Fatalf("strict Scanner query failed: %v", err)
			}
			got := rows[0].(*strictScannerDTO)
			if !got.Name.Valid || got.Name.String != "alice" || got.Count == nil || !got.Count.Valid || got.Count.Int64 != 7 {
				t.Fatalf("Scanner values mismatch: %#v", got)
			}
			rawPayload[0] = 'X'
			if string(got.Payload.Value) != "scanner-owned" {
				t.Fatalf("Scanner retained driver bytes: %q", got.Payload.Value)
			}

			nullState := newScriptedDBState(scriptedStep{
				kind:    "query",
				columns: []string{"name", "count"},
				rows:    [][]driver.Value{{nil, nil}},
			})
			nullRows, err := newStrictTestDb(t, nullState).ExecuteQueryStrictContext(
				context.Background(), "SELECT NULL scanner fields", nil, &strictScannerDTO{},
			)
			if err != nil {
				t.Fatalf("strict NULL Scanner query failed: %v", err)
			}
			nullGot := nullRows[0].(*strictScannerDTO)
			if nullGot.Name.Valid || nullGot.Count != nil {
				t.Fatalf("NULL Scanner semantics mismatch: %#v", nullGot)
			}
		})
	}
}

func TestStrictScannerErrorPropagates(t *testing.T) {
	for _, fast := range []bool{false, true} {
		mode := "legacy"
		if fast {
			mode = "fast"
		}
		t.Run(mode, func(t *testing.T) {
			applyStrictTestSettings(t, fast, false, 0)
			state := newScriptedDBState(scriptedStep{
				kind:    "query",
				columns: []string{"failure"},
				rows:    [][]driver.Value{{"bad"}},
			})
			rows, err := newStrictTestDb(t, state).ExecuteQueryStrictContext(
				context.Background(), "SELECT failing scanner", nil, &strictScannerDTO{},
			)
			if rows != nil || !errors.Is(err, errStrictScanner) {
				t.Fatalf("Scanner failure not preserved: rows=%#v err=%v", rows, err)
			}
		})
	}
}

func TestStrictInterfaceOwnsDriverBytes(t *testing.T) {
	for _, fast := range []bool{false, true} {
		mode := "legacy"
		if fast {
			mode = "fast"
		}
		t.Run(mode, func(t *testing.T) {
			applyStrictTestSettings(t, fast, false, 0)
			raw := []byte("owned")
			state := newScriptedDBState(scriptedStep{
				kind:    "query",
				columns: []string{"value"},
				rows:    [][]driver.Value{{raw}},
			})
			rows, err := newStrictTestDb(t, state).ExecuteQueryStrictContext(
				context.Background(), "SELECT interface bytes", nil, &strictByteOwnershipDTO{},
			)
			if err != nil {
				t.Fatalf("strict interface query failed: %v", err)
			}
			got, ok := rows[0].(*strictByteOwnershipDTO).Value.([]byte)
			if !ok {
				t.Fatalf("interface value type=%T, want []byte", rows[0].(*strictByteOwnershipDTO).Value)
			}
			raw[0] = 'X'
			if string(got) != "owned" {
				t.Fatalf("interface retained driver bytes: %q", got)
			}
		})
	}
}

func TestStrictIgnoredAnonymousFieldIsNotMapped(t *testing.T) {
	for _, fast := range []bool{false, true} {
		mode := "legacy"
		if fast {
			mode = "fast"
		}
		t.Run(mode, func(t *testing.T) {
			applyStrictTestSettings(t, fast, false, 0)
			state := newScriptedDBState(scriptedStep{
				kind:    "query",
				columns: []string{"value"},
				rows:    [][]driver.Value{{"must-be-ignored"}},
			})
			rows, err := newStrictTestDb(t, state).ExecuteQueryStrictContext(
				context.Background(), "SELECT ignored embedded", nil, &strictIgnoredEmbeddedDTO{},
			)
			if err != nil {
				t.Fatalf("strict ignored field query failed: %v", err)
			}
			if rows[0].(*strictIgnoredEmbeddedDTO).StrictIgnoredEmbeddedForTest != nil {
				t.Fatalf("db:- anonymous field was allocated: %#v", rows[0])
			}
		})
	}
}

func TestOrmScanPlanCacheIsBoundedAndConcurrent(t *testing.T) {
	var nilCache *OrmScanPlanCache
	if _, err := nilCache.GetStrictPlan(&struct{}{}, []string{"value"}); err == nil {
		t.Fatal("nil scan plan cache did not return an error")
	}

	cache := newOrmScanPlanCache(8)
	type cacheDTO struct {
		Value int `db:"value"`
	}
	prototype := &cacheDTO{}

	const goroutines = 32
	plans := make(chan *OrmScanPlan, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan, err := cache.GetStrictPlan(prototype, []string{"value"})
			if err != nil {
				t.Errorf("concurrent plan build: %v", err)
				return
			}
			plans <- plan
		}()
	}
	wg.Wait()
	close(plans)

	var first *OrmScanPlan
	for plan := range plans {
		if first == nil {
			first = plan
			continue
		}
		if plan != first {
			t.Fatal("concurrent callers received duplicate cached plan instances")
		}
	}

	for i := 0; i < 64; i++ {
		if _, err := cache.GetStrictPlan(prototype, []string{fmt.Sprintf("dynamic_%d", i)}); err != nil {
			t.Fatalf("build dynamic plan %d: %v", i, err)
		}
	}
	if got := cache.Len(); got != 8 {
		t.Fatalf("bounded cache size=%d, want 8", got)
	}
	cache.Clear()
	if got := cache.Len(); got != 0 {
		t.Fatalf("cleared cache size=%d, want 0", got)
	}
}

func TestClearScanScratchReferences(t *testing.T) {
	scratch := acquireScanScratch(2)
	marker := new(int)
	scratch.dest[0] = marker
	*scratch.discardPtr(0) = []byte("large driver value")
	clearScanScratchReferences(scratch)
	if scratch.dest[0] != nil || scratch.discards[0] != nil {
		t.Fatalf("scan scratch retained references: dest=%#v discard=%#v", scratch.dest[0], scratch.discards[0])
	}
	releaseScanScratch(scratch)
}

func strictSingleFieldQuery(
	t testing.TB,
	fast bool,
	source driver.Value,
	targetType reflect.Type,
) (reflect.Value, error) {
	t.Helper()
	applyStrictTestSettings(t, fast, false, 0)
	dtoType := reflect.StructOf([]reflect.StructField{{
		Name: "Value",
		Type: targetType,
		Tag:  reflect.StructTag(`db:"value"`),
	}})
	state := newScriptedDBState(scriptedStep{
		kind:    "query",
		columns: []string{"value"},
		rows:    [][]driver.Value{{source}},
	})
	rows, err := newStrictTestDb(t, state).ExecuteQueryStrictContext(
		context.Background(), "SELECT strict value", nil, reflect.New(dtoType).Interface(),
	)
	if err != nil {
		return reflect.Value{}, err
	}
	if len(rows) != 1 {
		return reflect.Value{}, fmt.Errorf("strict rows=%d, want 1", len(rows))
	}
	return reflect.ValueOf(rows[0]).Elem().Field(0), nil
}
