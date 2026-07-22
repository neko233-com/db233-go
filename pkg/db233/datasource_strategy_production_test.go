package db233

import "testing"

func TestSimpleDataSourceConfigUsesBoundConnectorAndPreservesCredentials(t *testing.T) {
	config, err := simpleMySQLDriverConfig(
		map[string]any{
			"host":     "2001:db8::1",
			"port":     3307,
			"username": "user:name",
			"password": "p@ss:word",
			"database": "game data",
			"params":   "interpolateParams=true&sql_mode=%27STRICT_ALL_TABLES%27",
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if config.User != "user:name" || config.Passwd != "p@ss:word" {
		t.Fatal("特殊凭据未原样保留")
	}
	if config.Addr != "[2001:db8::1]:3307" || config.DBName != "game data" || !config.InterpolateParams {
		t.Fatalf("Connector 配置错误: addr=%q db=%q interpolate=%v", config.Addr, config.DBName, config.InterpolateParams)
	}
	if config.Params["sql_mode"] != "'STRICT_ALL_TABLES'" {
		t.Fatalf("params 解析错误: %+v", config.Params)
	}
}

func TestSimpleDataSourceConfigRejectsMalformedValues(t *testing.T) {
	base := map[string]any{
		"host": "127.0.0.1", "port": 3306, "username": "root", "password": "secret", "database": "db",
	}
	for name, mutate := range map[string]func(map[string]any){
		"port":   func(config map[string]any) { config["port"] = 70000 },
		"params": func(config map[string]any) { config["params"] = map[int]string{1: "bad"} },
	} {
		t.Run(name, func(t *testing.T) {
			config := make(map[string]any, len(base))
			for key, value := range base {
				config[key] = value
			}
			mutate(config)
			if _, err := simpleMySQLDriverConfig(config, nil); err == nil {
				t.Fatal("非法配置应 fail-closed")
			}
		})
	}
}
