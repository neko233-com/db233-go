package db233

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"
)

var repositoryEntityShapeCache sync.Map // reflect.Type -> string

// validateRepositoryTableIdentifier 校验 CRUD 直接拼接的未引用表名。
// 支持常见的 schema.table；每段采用跨 MySQL/PostgreSQL 的保守标识符规则。
func validateRepositoryTableIdentifier(tableName string) error {
	if tableName == "" {
		return NewValidationException("表名不能为空")
	}
	segments := strings.Split(tableName, ".")
	for _, segment := range segments {
		if err := validateRepositoryIdentifierSegment("表名", segment); err != nil {
			return err
		}
	}
	return nil
}

func validateRepositoryColumnIdentifier(columnName string) error {
	return validateRepositoryIdentifierSegment("列名", columnName)
}

func validateRepositoryIdentifierSegment(kind string, name string) error {
	if name == "" {
		return NewValidationException(kind + "不能为空")
	}
	for index, current := range name {
		if index == 0 {
			if current != '_' && !unicode.IsLetter(current) {
				return NewValidationException(fmt.Sprintf("%s非法: %q", kind, name))
			}
			continue
		}
		if current != '_' && !unicode.IsLetter(current) && !unicode.IsDigit(current) && !unicode.IsMark(current) {
			return NewValidationException(fmt.Sprintf("%s非法: %q", kind, name))
		}
	}
	return nil
}

// validateRepositoryEntityIdentifiers 在执行 hook/WAL/SQL 前校验实体静态列元数据。
func validateRepositoryEntityIdentifiers(entity IDbEntity) error {
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	tableName := entity.TableName()
	if tableName == "" {
		entityType := reflect.TypeOf(entity)
		for entityType.Kind() == reflect.Ptr {
			entityType = entityType.Elem()
		}
		tableName = StringUtilsInstance.CamelToSnake(entityType.Name())
	}
	if err := validateRepositoryTableIdentifier(tableName); err != nil {
		return err
	}

	entityType := reflect.TypeOf(entity)
	for entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	if entityType.Kind() != reflect.Struct {
		return NewValidationException(fmt.Sprintf("实体必须是 struct 或 *struct，实际类型: %T", entity))
	}
	if err := validateRepositoryTypeColumns(entityType); err != nil {
		return err
	}
	primaryKey := GetCrudManagerInstance().GetPrimaryKeyColumnName(entity)
	if primaryKey == "" {
		primaryKey = "id"
	}
	return validateRepositoryColumnIdentifier(primaryKey)
}

// validateRepositoryBatchShapes 防止同表混合实体静默丢列或错用自增/主键语义。
func validateRepositoryBatchShapes(entities []IDbEntity, tableNameOf func(IDbEntity) string) error {
	shapesByTable := make(map[string]string)
	for index, entity := range entities {
		if isNilStrictValue(entity) {
			return NewValidationException(fmt.Sprintf("批量实体不能为 nil: 索引=%d", index))
		}
		tableName := tableNameOf(entity)
		shape, shapeErr := repositoryEntitySQLShape(entity)
		if shapeErr != nil {
			return shapeErr
		}
		if expected, exists := shapesByTable[tableName]; exists && expected != shape {
			return NewValidationException(fmt.Sprintf("同表批量写入的实体列结构不一致: table=%s, index=%d", tableName, index))
		}
		shapesByTable[tableName] = shape
	}
	return nil
}

func repositoryEntitySQLShape(entity IDbEntity) (string, error) {
	entityType := reflect.TypeOf(entity)
	for entityType.Kind() == reflect.Ptr {
		entityType = entityType.Elem()
	}
	if entityType.Kind() != reflect.Struct {
		return "", NewValidationException(fmt.Sprintf("实体必须是 struct 或 *struct，实际类型: %T", entity))
	}
	if err := validateRepositoryTypeColumns(entityType); err != nil {
		return "", err
	}
	if cached, exists := repositoryEntityShapeCache.Load(entityType); exists {
		return cached.(string), nil
	}
	parts := make([]string, 0, entityType.NumField())
	if err := collectRepositoryEntityShape(entityType, &parts, make(map[reflect.Type]bool)); err != nil {
		return "", err
	}
	sort.Strings(parts)
	shape := strings.Join(parts, "\x00")
	repositoryEntityShapeCache.Store(entityType, shape)
	return shape, nil
}

func collectRepositoryEntityShape(entityType reflect.Type, parts *[]string, visiting map[reflect.Type]bool) error {
	if visiting[entityType] {
		return NewValidationException(fmt.Sprintf("实体包含递归匿名嵌入结构: %s", entityType))
	}
	visiting[entityType] = true
	defer delete(visiting, entityType)

	cm := GetCrudManagerInstance()
	for index := 0; index < entityType.NumField(); index++ {
		field := entityType.Field(index)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				if err := collectRepositoryEntityShape(embeddedType, parts, visiting); err != nil {
					return err
				}
				continue
			}
		}
		columnName := cm.GetColumnName(field)
		if columnName == "" {
			continue
		}
		*parts = append(*parts, fmt.Sprintf("%s:%t:%t", columnName, cm.IsPrimaryKey(field), cm.IsAutoIncrement(field)))
	}
	return nil
}

func validateRepositoryTypeColumns(entityType reflect.Type) error {
	return validateRepositoryTypeColumnsRecursive(
		entityType,
		make(map[reflect.Type]bool),
		make(map[string]string),
	)
}

func validateRepositoryTypeColumnsRecursive(
	entityType reflect.Type,
	visiting map[reflect.Type]bool,
	columns map[string]string,
) error {
	if visiting[entityType] {
		return NewValidationException(fmt.Sprintf("实体包含递归匿名嵌入结构: %s", entityType))
	}
	visiting[entityType] = true
	defer delete(visiting, entityType)

	cm := GetCrudManagerInstance()
	for index := 0; index < entityType.NumField(); index++ {
		field := entityType.Field(index)
		if !field.IsExported() {
			continue
		}
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Ptr {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				if err := validateRepositoryTypeColumnsRecursive(embeddedType, visiting, columns); err != nil {
					return err
				}
				continue
			}
		}
		columnName := cm.GetColumnName(field)
		if columnName == "" {
			continue
		}
		if err := validateRepositoryColumnIdentifier(columnName); err != nil {
			return err
		}
		fieldPath := entityType.String() + "." + field.Name
		if existing, exists := columns[columnName]; exists {
			return NewValidationException(fmt.Sprintf(
				"实体列映射重复: column=%s, fields=%s,%s",
				columnName,
				existing,
				fieldPath,
			))
		}
		columns[columnName] = fieldPath
	}
	return nil
}

func validateRepositorySQLIdentifiers(tableName string, primaryKey string, columns []string) error {
	if err := validateRepositoryTableIdentifier(tableName); err != nil {
		return err
	}
	if primaryKey != "" {
		if err := validateRepositoryColumnIdentifier(primaryKey); err != nil {
			return err
		}
	}
	for _, column := range columns {
		if err := validateRepositoryColumnIdentifier(column); err != nil {
			return err
		}
	}
	return nil
}
