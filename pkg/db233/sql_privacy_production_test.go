package db233

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
)

func TestSQLPrivacy_RuntimeLogsAndErrorsAreRedactedByDefault(t *testing.T) {
	previousLogger := defaultLogger
	previousFullSQL := IsLogFullSQLEnabled()
	var output bytes.Buffer
	defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
	SetLogFullSQL(false)
	t.Cleanup(func() {
		defaultLogger = previousLogger
		SetLogFullSQL(previousFullSQL)
	})

	literalSecret := "literal-production-secret"
	parameterSecret := "parameter-production-secret"
	query := "UPDATE users SET token='" + literalSecret + "'\nFORGED-RECORD WHERE id=?"
	driverErr := errors.New("driver rejected parameter " + parameterSecret + " while executing " + query)
	state := newScriptedDBState(scriptedStep{kind: "exec", queryContains: "UPDATE users", execErr: driverErr})
	db := NewDb(openScriptedDB(t, state), 0, nil)
	t.Cleanup(func() { _ = db.Close() })

	_, err := db.ExecuteUpdate(query, parameterSecret)
	if err == nil {
		t.Fatal("expected update failure")
	}
	LogWarn("raw driver error must be centrally redacted: %v", driverErr)
	if !errors.Is(err, driverErr) {
		t.Fatalf("redaction must preserve errors.Is: %v", err)
	}
	genericErr := NewDb233ExceptionWithCause(driverErr, "generic failure")
	if !errors.Is(genericErr, driverErr) {
		t.Fatalf("generic redaction must preserve errors.Is: %v", genericErr)
	}
	assertTextDoesNotContainSecrets(t, "default log", output.String(), literalSecret, parameterSecret, "FORGED-RECORD")
	assertTextDoesNotContainSecrets(t, "returned error", err.Error(), literalSecret, parameterSecret, "FORGED-RECORD")
	assertTextDoesNotContainSecrets(t, "generic returned error", genericErr.Error(), literalSecret, parameterSecret, "FORGED-RECORD")
	duplicateErr := NewDb233ExceptionWithCause(errors.New("Duplicate entry '"+parameterSecret+"'"), "save failed")
	if !strings.Contains(duplicateErr.Error(), "数据重复冲突") {
		t.Fatalf("friendly classification lost after redaction: %q", duplicateErr.Error())
	}
	assertTextDoesNotContainSecrets(t, "friendly returned error", duplicateErr.Error(), parameterSecret)
	connectionCause := errors.New("connection reset while sending " + parameterSecret)
	connectionErr := NewQueryExceptionWithCause(connectionCause, "query failed")
	if !isConnectionError(connectionErr) {
		t.Fatal("redaction must not hide connection classification")
	}
	assertTextDoesNotContainSecrets(t, "connection error", connectionErr.Error(), parameterSecret)
	if !strings.Contains(output.String(), "SQLVerb: UPDATE") || strings.Contains(output.String(), "SQLHash:") || strings.Contains(output.String(), "SQLLength:") {
		t.Fatalf("default log lacks SQL summary: %q", output.String())
	}
	if !strings.Contains(err.Error(), "SQLVerb: UPDATE") || !strings.Contains(err.Error(), "原因摘要:") {
		t.Fatalf("returned error lacks safe diagnostics: %q", err.Error())
	}
}

func TestSQLPrivacy_FullSQLIsLogOnlyOptInAndControlSafe(t *testing.T) {
	previousLogger := defaultLogger
	previousFullSQL := IsLogFullSQLEnabled()
	var output bytes.Buffer
	defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
	SetLogFullSQL(true)
	t.Cleanup(func() {
		defaultLogger = previousLogger
		SetLogFullSQL(previousFullSQL)
	})

	literalSecret := "opt-in-literal-secret"
	parameterSecret := "never-log-parameter-secret"
	query := "UPDATE users SET token='" + literalSecret + "'\nFORGED-RECORD WHERE id=?"
	driverErr := errors.New("driver value=" + parameterSecret + ", query=" + query)
	state := newScriptedDBState(scriptedStep{kind: "exec", queryContains: "UPDATE users", execErr: driverErr})
	db := NewDb(openScriptedDB(t, state), 0, nil)
	t.Cleanup(func() { _ = db.Close() })

	_, err := db.ExecuteUpdate(query, parameterSecret)
	if err == nil {
		t.Fatal("expected update failure")
	}
	logged := output.String()
	if !strings.Contains(logged, literalSecret) {
		t.Fatalf("explicit opt-in did not include quoted SQL: %q", logged)
	}
	if strings.Contains(logged, parameterSecret) {
		t.Fatalf("SQL parameter leaked after full-SQL opt-in: %q", logged)
	}
	if strings.Contains(logged, "\nFORGED-RECORD") {
		t.Fatalf("control character forged an additional log line: %q", logged)
	}
	if !strings.Contains(logged, `\nFORGED-RECORD`) {
		t.Fatalf("full SQL was not safely quoted: %q", logged)
	}
	assertTextDoesNotContainSecrets(t, "opt-in returned error", err.Error(), literalSecret, parameterSecret, "FORGED-RECORD")
}

func TestSQLPrivacy_DisabledLogPathDoesNotRenderSQL(t *testing.T) {
	logger := newLogger(ERROR, log.New(io.Discard, "", 0))

	allocations := testing.AllocsPerRun(1000, func() {
		logger.logRuntime(DEBUG, "table=%s query=%s", "private-table", sqlForRuntimeLog("SELECT 'success-hot-path'"))
	})
	if allocations != 0 {
		t.Fatalf("disabled SQL log path allocated: %.2f allocs/run", allocations)
	}
}

func TestSQLPrivacy_RuntimeHelpersRedactBareStrings(t *testing.T) {
	previousLogger := defaultLogger
	var output bytes.Buffer
	defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
	t.Cleanup(func() { defaultLogger = previousLogger })

	type privateName string
	secrets := []string{"private_table", "private_column", "player-secret", "private/path", "named-string-secret"}
	LogInfo("table=%s column=%s player=%s path=%s named=%s", secrets[0], secrets[1], secrets[2], secrets[3], privateName(secrets[4]))

	logged := output.String()
	assertTextDoesNotContainSecrets(t, "runtime helper log", logged, secrets...)
	if strings.Count(logged, "ValueType=") != len(secrets) {
		t.Fatalf("runtime string redaction missing: %q", logged)
	}
}

func TestSQLPrivacy_LoggerEscapesControlCharactersInArbitraryFields(t *testing.T) {
	var output bytes.Buffer
	logger := newLogger(TRACE, log.New(&output, "", 0))

	logger.Warn("name=%s", "player-1\r\nFORGED-RECORD")

	logged := output.String()
	if strings.Contains(logged, "\r\nFORGED-RECORD") || strings.Count(logged, "\n") != 1 {
		t.Fatalf("arbitrary field forged an additional log record: %q", logged)
	}
	if !strings.Contains(logged, `player-1\r\nFORGED-RECORD`) {
		t.Fatalf("control characters were not escaped: %q", logged)
	}
}

func TestPublicSafeSummariesNeverExposeSecretsOrControlCharacters(t *testing.T) {
	secret := "Duplicate entry 'api-token-123'\r\nFORGED-RECORD"
	cause := errors.New(secret)
	errorSummary := SafeErrorSummary(cause)
	valueSummary := SafeValueSummary(secret)
	for name, summary := range map[string]string{
		"error": errorSummary,
		"value": valueSummary,
	} {
		assertTextDoesNotContainSecrets(t, name, summary, "api-token-123", "Duplicate entry", "FORGED-RECORD", "\r", "\n")
		if !strings.Contains(summary, "Type=") || strings.Contains(summary, "Hash=") || strings.Contains(summary, "Length=") {
			t.Fatalf("%s summary lacks diagnostics: %q", name, summary)
		}
	}
	if !strings.Contains(errorSummary, "ErrorClass=duplicate_key") {
		t.Fatalf("stable error class missing: %q", errorSummary)
	}
	if got := SafeErrorSummary(nil); got != "ErrorType=<nil>" {
		t.Fatalf("nil summary=%q", got)
	}
	wrapper := NewQueryExceptionWithCause(cause, "safe")
	if !errors.Is(wrapper, cause) {
		t.Fatal("summary API changes must not disturb errors.Is chains")
	}
}

func TestSQLPrivacy_PerformanceMonitorDoesNotRetainDriverSecrets(t *testing.T) {
	canary := "monitor-private-driver-value"
	monitor := NewPerformanceMonitor("production", nil)
	monitor.RecordQuery("SELECT '"+canary+"'", 10, false, errors.New("driver echoed "+canary))

	monitor.mu.RLock()
	if len(monitor.lastErrors) != 1 {
		monitor.mu.RUnlock()
		t.Fatalf("stored errors=%d, want 1", len(monitor.lastErrors))
	}
	stored := monitor.lastErrors[0]
	monitor.mu.RUnlock()
	assertTextDoesNotContainSecrets(t, "stored monitor error", stored.Error.Error(), canary)
	assertTextDoesNotContainSecrets(t, "stored monitor query", stored.Query, canary)

	report, err := json.Marshal(monitor.GetDetailedReport())
	if err != nil {
		t.Fatal(err)
	}
	assertTextDoesNotContainSecrets(t, "monitor report", string(report), canary)
}

func assertTextDoesNotContainSecrets(t *testing.T, name, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked %q: %q", name, secret, text)
		}
	}
}
