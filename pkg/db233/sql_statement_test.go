package db233

import (
	"testing"
)

/**
 * SqlStatement 单元测试
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */
func TestNewQueryStatement(t *testing.T) {
	stmt := NewQueryStatement("SELECT * FROM user", "User")

	if !stmt.IsQuery {
		t.Error("查询语句的 IsQuery 应该为 true")
	}

	if stmt.IsAutoCommit != true {
		t.Error("默认 IsAutoCommit 应该为 true")
	}

	if len(stmt.SqlList) != 1 || stmt.SqlList[0] != "SELECT * FROM user" {
		t.Error("SQL 列表不正确")
	}

	if stmt.ReturnType != "User" {
		t.Error("返回类型不正确")
	}
}

func TestNewQueryStatements(t *testing.T) {
	sqlList := []string{"SELECT * FROM user", "SELECT * FROM order"}
	stmt := NewQueryStatements(sqlList, "User")

	if !stmt.IsQuery {
		t.Error("查询语句的 IsQuery 应该为 true")
	}

	if len(stmt.SqlList) != 2 {
		t.Error("SQL 列表长度不正确")
	}

	if stmt.ReturnType != "User" {
		t.Error("返回类型不正确")
	}
}

func TestNewUpdateStatement(t *testing.T) {
	stmt := NewUpdateStatement("UPDATE user SET name = ?")

	if stmt.IsQuery {
		t.Error("更新语句的 IsQuery 应该为 false")
	}

	if stmt.IsAutoCommit != true {
		t.Error("默认 IsAutoCommit 应该为 true")
	}

	if len(stmt.SqlList) != 1 || stmt.SqlList[0] != "UPDATE user SET name = ?" {
		t.Error("SQL 列表不正确")
	}

	if stmt.ReturnType != nil {
		t.Error("更新语句的返回类型应该为 nil")
	}
}

func TestNewUpdateStatements(t *testing.T) {
	sqlList := []string{"UPDATE user SET name = ?", "DELETE FROM user WHERE id = ?"}
	stmt := NewUpdateStatements(sqlList)

	if stmt.IsQuery {
		t.Error("更新语句的 IsQuery 应该为 false")
	}

	if len(stmt.SqlList) != 2 {
		t.Error("SQL 列表长度不正确")
	}

	if stmt.ReturnType != nil {
		t.Error("更新语句的返回类型应该为 nil")
	}
}
