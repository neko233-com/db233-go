package db233

import (
	"fmt"
)

// TableMetaData 表元数据，包含表名和索引信息。
type TableMetaData struct {
	TableName string
	Indexes   []*IndexMetaData
}

// IndexMetaData 索引元数据。
type IndexMetaData struct {
	IndexName string
	Columns   []string
	IsUnique  bool
}

// IndexBuilder 索引构建器，使用 builder 模式。
type IndexBuilder struct {
	tableName string
	indexes   []*IndexMetaData
	current   *IndexMetaData
}

// NewIndexBuilder 创建索引构建器。
func NewIndexBuilder(tableName string) *IndexBuilder {
	return &IndexBuilder{
		tableName: tableName,
		indexes:   make([]*IndexMetaData, 0),
	}
}

// AddNewIndexName 添加新索引名称。
func (b *IndexBuilder) AddNewIndexName(indexName string) *IndexBuilder {
	// 如果当前索引有列，先完成它
	if b.current != nil && len(b.current.Columns) > 0 {
		b.indexes = append(b.indexes, b.current)
	}

	b.current = &IndexMetaData{
		IndexName: indexName,
		Columns:   make([]string, 0),
		IsUnique:  false,
	}
	return b
}

// AddIndexColumn 添加索引列。
func (b *IndexBuilder) AddIndexColumn(columnName string) *IndexBuilder {
	if b.current == nil {
		// 如果没有当前索引，创建一个默认名称的索引
		b.current = &IndexMetaData{
			IndexName: fmt.Sprintf("idx_%s_%s", b.tableName, columnName),
			Columns:   make([]string, 0),
			IsUnique:  false,
		}
	}
	b.current.Columns = append(b.current.Columns, columnName)
	return b
}

// SetUnique 设置索引为唯一索引。
func (b *IndexBuilder) SetUnique(isUnique bool) *IndexBuilder {
	if b.current != nil {
		b.current.IsUnique = isUnique
	}
	return b
}

// DoneIndex 完成当前索引。
func (b *IndexBuilder) DoneIndex() *IndexBuilder {
	if b.current != nil && len(b.current.Columns) > 0 {
		b.indexes = append(b.indexes, b.current)
		b.current = nil
	}
	return b
}

// Build 构建 TableMetaData。
func (b *IndexBuilder) Build() *TableMetaData {
	// 完成最后一个索引
	if b.current != nil && len(b.current.Columns) > 0 {
		b.indexes = append(b.indexes, b.current)
		b.current = nil
	}

	return &TableMetaData{
		TableName: b.tableName,
		Indexes:   b.indexes,
	}
}

// GetTableMetaData 获取表元数据（从 ITableMetaDataProvider 接口）。
// 如果实体实现了 ITableMetaDataProvider 接口，返回元数据；否则返回 nil。
func GetTableMetaData(entity IDbEntity) *TableMetaData {
	if entity == nil {
		return nil
	}

	// 检查是否实现了 ITableMetaDataProvider 接口
	if provider, ok := entity.(ITableMetaDataProvider); ok {
		return provider.GetTableMetaData()
	}

	return nil
}
