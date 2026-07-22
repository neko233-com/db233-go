package db233

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const scriptedDriverName = "db233-scripted"

var (
	scriptedDriverOnce sync.Once
	scriptedDSNSeq     atomic.Uint64
	scriptedDatabases  sync.Map
)

type scriptedStep struct {
	kind           string
	queryContains  string
	columns        []string
	rows           [][]driver.Value
	driverEntered  chan<- struct{}
	driverRelease  <-chan struct{}
	respectContext bool
	queryErr       error
	rowErrAt       int
	rowErr         error
	closeErr       error
	closeNotify    chan<- struct{}
	result         driver.Result
	execErr        error
}

type scriptedCall struct {
	kind    string
	query   string
	args    []driver.NamedValue
	txID    int
	options driver.TxOptions
}

type scriptedDBState struct {
	mu sync.Mutex

	steps         []scriptedStep
	repeatQuery   *scriptedStep
	calls         []scriptedCall
	suppressCalls bool

	beginErr    error
	beginCtx    context.Context
	commitErr   error
	rollbackErr error
	nextTxID    int

	prepareEntered        chan<- struct{}
	prepareRelease        <-chan struct{}
	prepareErr            error
	prepareRespectContext bool
	stmtCloseErr          error
	stmtCloseCalls        int

	pingEntered        chan<- struct{}
	pingRelease        <-chan struct{}
	pingErr            error
	pingRespectContext bool
	pingCalls          int
}

func newScriptedDBState(steps ...scriptedStep) *scriptedDBState {
	return &scriptedDBState{steps: append([]scriptedStep(nil), steps...)}
}

func (s *scriptedDBState) takeStep(kind, query string, args []driver.NamedValue, txID int) (scriptedStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.suppressCalls {
		s.calls = append(s.calls, scriptedCall{
			kind:  kind,
			query: query,
			args:  append([]driver.NamedValue(nil), args...),
			txID:  txID,
		})
	}

	if len(s.steps) == 0 {
		if kind == "query" && s.repeatQuery != nil {
			return *s.repeatQuery, nil
		}
		return scriptedStep{}, fmt.Errorf("scripted driver: unexpected %s: %s", kind, query)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.kind != kind {
		return scriptedStep{}, fmt.Errorf("scripted driver: expected %s, got %s: %s", step.kind, kind, query)
	}
	if step.queryContains != "" && !strings.Contains(query, step.queryContains) {
		return scriptedStep{}, fmt.Errorf("scripted driver: query %q does not contain %q", query, step.queryContains)
	}
	return step, nil
}

func (s *scriptedDBState) recordTerminal(kind string, txID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.suppressCalls {
		s.calls = append(s.calls, scriptedCall{kind: kind, txID: txID})
	}
	if kind == "commit" {
		return s.commitErr
	}
	return s.rollbackErr
}

func (s *scriptedDBState) recordBegin(ctx context.Context, options driver.TxOptions) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginCtx = ctx
	if s.beginErr != nil {
		if !s.suppressCalls {
			s.calls = append(s.calls, scriptedCall{kind: "begin", options: options})
		}
		return 0, s.beginErr
	}
	s.nextTxID++
	txID := s.nextTxID
	if !s.suppressCalls {
		s.calls = append(s.calls, scriptedCall{kind: "begin", txID: txID, options: options})
	}
	return txID, nil
}

func (s *scriptedDBState) snapshotBeginContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beginCtx
}

func (s *scriptedDBState) recordPrepare(ctx context.Context, query string, txID int) error {
	s.mu.Lock()
	if !s.suppressCalls {
		s.calls = append(s.calls, scriptedCall{kind: "prepare", query: query, txID: txID})
	}
	entered := s.prepareEntered
	release := s.prepareRelease
	prepareErr := s.prepareErr
	respectContext := s.prepareRespectContext
	s.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		if respectContext {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			<-release
		}
	}
	return prepareErr
}

func (s *scriptedDBState) recordPing(ctx context.Context) error {
	s.mu.Lock()
	s.pingCalls++
	if !s.suppressCalls {
		s.calls = append(s.calls, scriptedCall{kind: "ping"})
	}
	entered := s.pingEntered
	release := s.pingRelease
	pingErr := s.pingErr
	respectContext := s.pingRespectContext
	s.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		if respectContext {
			select {
			case <-release:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		} else {
			<-release
		}
	}
	return pingErr
}

func (s *scriptedDBState) recordStmtClose() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stmtCloseCalls++
	return s.stmtCloseErr
}

func (s *scriptedDBState) snapshotCalls() []scriptedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]scriptedCall, len(s.calls))
	for i, call := range s.calls {
		result[i] = call
		result[i].args = append([]driver.NamedValue(nil), call.args...)
	}
	return result
}

func (s *scriptedDBState) countCalls(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, call := range s.calls {
		if call.kind == kind {
			count++
		}
	}
	return count
}

type scriptedDriver struct{}

func (scriptedDriver) Open(name string) (driver.Conn, error) {
	value, ok := scriptedDatabases.Load(name)
	if !ok {
		return nil, fmt.Errorf("scripted driver: unknown database %q", name)
	}
	return &scriptedConn{state: value.(*scriptedDBState)}, nil
}

type scriptedConn struct {
	state *scriptedDBState
	txID  int
}

func (c *scriptedConn) Prepare(query string) (driver.Stmt, error) {
	if err := c.state.recordPrepare(context.Background(), query, c.txID); err != nil {
		return nil, err
	}
	return &scriptedStmt{conn: c, query: query}, nil
}

func (c *scriptedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.state.recordPrepare(ctx, query, c.txID); err != nil {
		return nil, err
	}
	return &scriptedStmt{conn: c, query: query}, nil
}

func (c *scriptedConn) Close() error { return nil }

func (c *scriptedConn) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.state.recordPing(ctx)
}

func (c *scriptedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *scriptedConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	txID, err := c.state.recordBegin(ctx, options)
	if err != nil {
		return nil, err
	}
	c.txID = txID
	return &scriptedTx{conn: c, txID: txID}, nil
}

func (c *scriptedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step, err := c.state.takeStep("query", query, args, c.txID)
	if err != nil {
		return nil, err
	}
	if err := waitForScriptedDriverStep(ctx, step); err != nil {
		return nil, err
	}
	if step.queryErr != nil {
		return nil, step.queryErr
	}
	return &scriptedRows{
		columns:     append([]string(nil), step.columns...),
		rows:        step.rows,
		rowErrAt:    step.rowErrAt,
		rowErr:      step.rowErr,
		closeErr:    step.closeErr,
		closeNotify: step.closeNotify,
	}, nil
}

func (c *scriptedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step, err := c.state.takeStep("exec", query, args, c.txID)
	if err != nil {
		return nil, err
	}
	if err := waitForScriptedDriverStep(ctx, step); err != nil {
		return nil, err
	}
	if step.execErr != nil {
		return nil, step.execErr
	}
	if step.result == nil {
		return driver.RowsAffected(1), nil
	}
	return step.result, nil
}

type scriptedStmt struct {
	conn  *scriptedConn
	query string
}

func (s *scriptedStmt) Close() error { return s.conn.state.recordStmtClose() }

func (s *scriptedStmt) NumInput() int { return -1 }

func (s *scriptedStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamedValues(args))
}

func (s *scriptedStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamedValues(args))
}

func (s *scriptedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}

func (s *scriptedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}

func valuesToNamedValues(values []driver.Value) []driver.NamedValue {
	result := make([]driver.NamedValue, len(values))
	for index, value := range values {
		result[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return result
}

func waitForScriptedDriverStep(ctx context.Context, step scriptedStep) error {
	if step.driverEntered != nil {
		step.driverEntered <- struct{}{}
	}
	if step.driverRelease != nil {
		if step.respectContext {
			select {
			case <-step.driverRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			<-step.driverRelease
		}
	}
	return nil
}

type scriptedTx struct {
	conn *scriptedConn
	txID int
}

func (tx *scriptedTx) Commit() error {
	tx.conn.txID = 0
	return tx.conn.state.recordTerminal("commit", tx.txID)
}

func (tx *scriptedTx) Rollback() error {
	tx.conn.txID = 0
	return tx.conn.state.recordTerminal("rollback", tx.txID)
}

type scriptedRows struct {
	columns []string
	rows    [][]driver.Value
	index   int

	rowErrAt    int
	rowErr      error
	closeErr    error
	closeNotify chan<- struct{}
	closed      bool
}

func (r *scriptedRows) Columns() []string {
	return append([]string(nil), r.columns...)
}

func (r *scriptedRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.closeNotify != nil {
		select {
		case r.closeNotify <- struct{}{}:
		default:
		}
	}
	return r.closeErr
}

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.rowErr != nil && r.index == r.rowErrAt {
		return r.rowErr
	}
	if r.index >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.index]
	if len(row) != len(dest) {
		return fmt.Errorf("scripted driver: row has %d values, want %d", len(row), len(dest))
	}
	copy(dest, row)
	r.index++
	return nil
}

type scriptedResult struct {
	lastInsertID    int64
	lastInsertIDErr error
	rowsAffected    int64
	rowsAffectedErr error
}

func (r scriptedResult) LastInsertId() (int64, error) {
	return r.lastInsertID, r.lastInsertIDErr
}

func (r scriptedResult) RowsAffected() (int64, error) {
	return r.rowsAffected, r.rowsAffectedErr
}

func openScriptedDB(t testing.TB, state *scriptedDBState) *sql.DB {
	t.Helper()
	scriptedDriverOnce.Do(func() {
		sql.Register(scriptedDriverName, scriptedDriver{})
	})
	dsn := fmt.Sprintf("case-%d", scriptedDSNSeq.Add(1))
	scriptedDatabases.Store(dsn, state)
	db, err := sql.Open(scriptedDriverName, dsn)
	if err != nil {
		t.Fatalf("open scripted database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		scriptedDatabases.Delete(dsn)
	})
	return db
}
