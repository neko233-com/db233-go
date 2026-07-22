package db233

import (
	"strings"
	"testing"
	"time"
)

func TestMySQLDriverConfigPreservesCredentialsAndParsesParameters(t *testing.T) {
	config := NewDefaultMySQLConfig("2001:db8::1", 3307, "user:name", "p@ss:word", "game data")
	config.Loc = "Asia/Shanghai"
	config.ExtraParams = map[string]string{
		"interpolateParams": "true",
		"sql_mode":          "'STRICT_ALL_TABLES'",
	}
	driverConfig, err := config.mysqlDriverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if driverConfig.User != config.Username || driverConfig.Passwd != config.Password {
		t.Fatal("Connector 配置未原样保留特殊凭据")
	}
	if driverConfig.Addr != "[2001:db8::1]:3307" || driverConfig.DBName != "game data" {
		t.Fatalf("地址或库名解析错误: addr=%q db=%q", driverConfig.Addr, driverConfig.DBName)
	}
	if !driverConfig.InterpolateParams || !driverConfig.ParseTime || driverConfig.Loc.String() != "Asia/Shanghai" {
		t.Fatalf("驱动参数未正确解析: %+v", driverConfig)
	}
	if driverConfig.Params["sql_mode"] != "'STRICT_ALL_TABLES'" {
		t.Fatalf("服务端参数丢失: %+v", driverConfig.Params)
	}
}

func TestConnectionConfigRejectsUnsafeParameterKey(t *testing.T) {
	config := NewDefaultMySQLConfig("127.0.0.1", 3306, "root", "secret", "db")
	config.ExtraParams = map[string]string{"sql_mode, injected": "1"}
	if _, err := config.mysqlDriverConfig(); err == nil {
		t.Fatal("非法扩展参数名应 fail-closed")
	}
	config.Port = 70000
	config.ExtraParams = nil
	if _, err := config.mysqlDriverConfig(); err == nil {
		t.Fatal("非法端口应 fail-closed")
	}
}

func TestPostgreSQLDSNIsDeterministicAndQuoted(t *testing.T) {
	config := NewDefaultPostgreSQLConfig("db.internal", 5432, "user name", "p'ass\\word", "game db")
	config.ConnectTimeout = 1500 * time.Millisecond
	first := config.buildPostgreSQLDSN()
	for i := 0; i < 20; i++ {
		if got := config.buildPostgreSQLDSN(); got != first {
			t.Fatalf("PostgreSQL DSN 非确定性: %q != %q", got, first)
		}
	}
	if !strings.Contains(first, `password='p\'ass\\word'`) || !strings.Contains(first, `dbname='game db'`) {
		t.Fatalf("PostgreSQL DSN 未安全引用: %s", first)
	}
	if !strings.Contains(first, `connect_timeout='2'`) {
		t.Fatalf("亚秒超时应向上取整，避免被驱动解释为无限等待: %s", first)
	}
}

func TestPostgreSQLConfigRejectsUnsafeParameterKeyAndPort(t *testing.T) {
	config := NewDefaultPostgreSQLConfig("127.0.0.1", 5432, "postgres", "secret", "db")
	config.ExtraParams = map[string]string{"sslmode injected": "disable"}
	if dsn := config.BuildDSN(); dsn != "" {
		t.Fatalf("非法参数名应 fail-closed，got=%q", dsn)
	}
	if _, err := config.postgreSQLDSN(); err == nil {
		t.Fatal("非法参数名应返回错误")
	}
	config.ExtraParams = nil
	config.Port = 70000
	if _, err := config.postgreSQLDSN(); err == nil {
		t.Fatal("非法端口应返回错误")
	}
}

func TestNilConnectionConfigFailsClosed(t *testing.T) {
	var config *DbConnectionConfig
	if _, err := config.CreateDataSource(); err == nil {
		t.Fatal("nil 配置不应 panic 或成功")
	}
}
