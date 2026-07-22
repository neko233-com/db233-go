package db233

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMonitoringReportExportsArePrivateAtomicAndComplete(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private", "reports")
			path := filepath.Join(dir, "monitoring."+format)
			generator := NewMonitoringReportGenerator("export-test")
			generator.SetReportTitle("business-monitor-canary-old")
			if err := generator.ExportReport(path, format); err != nil {
				t.Fatalf("首次导出失败: %v", err)
			}
			assertFileContains(t, path, "business-monitor-canary-old")

			generator.SetReportTitle("business-monitor-canary-new")
			if err := generator.ExportReport(path, format); err != nil {
				t.Fatalf("原子覆盖失败: %v", err)
			}
			assertFileContains(t, path, "business-monitor-canary-new")
			assertPrivateExportPermissions(t, path, dir)
			assertNoSecureAtomicTemps(t, dir)
		})
	}
}

func TestMetricsCollectorExportIsPrivateAtomicAndComplete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private", "metrics")
	path := filepath.Join(dir, "metrics.json")
	collector := NewMetricsCollector("business-collector-canary")
	collector.mu.Lock()
	collector.metricsData["business.metric.canary"] = []MetricPoint{{
		Timestamp: time.Now().UTC(),
		Name:      "business.metric.canary",
		Value:     233.0,
		Tags:      map[string]string{"scope": "business-tag-canary"},
	}}
	collector.mu.Unlock()

	if err := collector.ExportToFile(path); err != nil {
		t.Fatalf("指标导出失败: %v", err)
	}
	for _, canary := range []string{
		"business-collector-canary",
		"business.metric.canary",
		"business-tag-canary",
	} {
		assertFileContains(t, path, canary)
	}
	assertPrivateExportPermissions(t, path, dir)
	assertNoSecureAtomicTemps(t, dir)
}

func TestSecureAtomicExportFailuresPreserveOldFileAndCleanTemp(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*secureAtomicFileOps, error)
	}{
		{
			name: "create-temp",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				ops.createTemp = func(string, string) (*os.File, error) { return nil, failure }
			},
		},
		{
			name: "chmod-temp",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				ops.chmodTemp = func(*os.File, os.FileMode) error { return failure }
			},
		},
		{
			name: "harden-existing",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				ops.hardenFile = func(string) error { return failure }
			},
		},
		{
			name: "harden-temp",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				hardenFile := ops.hardenFile
				calls := 0
				ops.hardenFile = func(path string) error {
					calls++
					if calls == 1 {
						return hardenFile(path)
					}
					return failure
				}
			},
		},
		{
			name: "write",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				ops.writeAll = func(*os.File, []byte) error { return failure }
			},
		},
		{
			name: "sync",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				ops.syncTemp = func(*os.File) error { return failure }
			},
		},
		{
			name: "close",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				closeTemp := ops.closeTemp
				ops.closeTemp = func(file *os.File) error {
					return errors.Join(closeTemp(file), failure)
				}
			},
		},
		{
			name: "replace",
			mutate: func(ops *secureAtomicFileOps, failure error) {
				ops.replace = func(string, string) error { return failure }
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "existing.json")
			oldContent := []byte("old-monitoring-content")
			if err := os.WriteFile(path, oldContent, 0644); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("injected export failure")
			ops := defaultSecureAtomicFileOps()
			test.mutate(&ops, failure)

			err := writeSecureAtomicFileWithOps(path, []byte("new-monitoring-content"), ops)
			if !errors.Is(err, failure) {
				t.Fatalf("错误未传播: %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(oldContent) {
				t.Fatalf("失败后旧文件被改写: %q", got)
			}
			assertNoSecureAtomicTemps(t, dir)
		})
	}
}

func TestMonitoringExportsDoNotLogPrivatePathOrForgeRecords(t *testing.T) {
	previousLogger := defaultLogger
	var output bytes.Buffer
	defaultLogger = newLogger(TRACE, log.New(&output, "", 0))
	t.Cleanup(func() { defaultLogger = previousLogger })

	dir := filepath.Join(t.TempDir(), "private-path-canary")
	generator := NewMonitoringReportGenerator("log-test")
	if err := generator.ExportReport(filepath.Join(dir, "report.json"), "json"); err != nil {
		t.Fatal(err)
	}
	collector := NewMetricsCollector("log-test")
	controlPath := filepath.Join(dir, "metrics-private-path-canary\r\nFORGED-EXPORT-LOG.json")
	err := collector.ExportToFile(controlPath)
	if err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}

	logged := output.String()
	for _, secret := range []string{"private-path-canary", "FORGED-EXPORT-LOG"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("运行时导出日志泄露路径或可伪造记录: %q", logged)
		}
	}
}

func TestSecureAtomicExportJoinsCleanupError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	replaceFailure := errors.New("replace failure")
	cleanupFailure := errors.New("cleanup failure")
	ops := defaultSecureAtomicFileOps()
	ops.replace = func(string, string) error { return replaceFailure }
	removeTemp := ops.removeTemp
	ops.removeTemp = func(path string) error {
		return errors.Join(removeTemp(path), cleanupFailure)
	}

	err := writeSecureAtomicFileWithOps(path, []byte("new"), ops)
	if !errors.Is(err, replaceFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("替换/清理错误未 Join: %v", err)
	}
	assertFileContains(t, path, "old")
	assertNoSecureAtomicTemps(t, dir)
}

func TestSecureAtomicExportPropagatesDirectorySyncFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	failure := errors.New("directory sync failure")
	ops := defaultSecureAtomicFileOps()
	ops.syncDirectory = func(string) error { return failure }

	err := writeSecureAtomicFileWithOps(path, []byte("durable-candidate"), ops)
	if !errors.Is(err, failure) {
		t.Fatalf("目录同步错误未传播: %v", err)
	}
	assertFileContains(t, path, "durable-candidate")
	assertNoSecureAtomicTemps(t, dir)
}

type panickingExportJSON struct{}

func (panickingExportJSON) MarshalJSON() ([]byte, error) {
	panic("private-json-canary")
}

func TestSecureExportJSONConvertsPanicWithoutLeakingValue(t *testing.T) {
	_, err := marshalSecureExportJSON(panickingExportJSON{})
	if err == nil {
		t.Fatal("期望序列化 panic 转为 error")
	}
	if strings.Contains(err.Error(), "private-json-canary") {
		t.Fatalf("错误泄露 panic 内容: %v", err)
	}
}

func assertFileContains(t *testing.T, path, canary string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), canary) {
		t.Fatalf("导出缺少 canary %q", canary)
	}
}

func assertPrivateExportPermissions(t *testing.T, path, createdDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != recoveryFileMode {
		t.Fatalf("导出文件权限=%v, want=%v", got, recoveryFileMode)
	}
	dirInfo, err := os.Stat(createdDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != recoveryDirectoryMode {
		t.Fatalf("新建导出目录权限=%v, want=%v", got, recoveryDirectoryMode)
	}
}

func assertNoSecureAtomicTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, secureAtomicTempPattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("残留安全导出临时文件: %v", matches)
	}
}
