package db233

// appendBatchUpsertSQL 池化 Builder 拼接批量 UPSERT / INSERT IGNORE SQL。
func appendBatchUpsertSQL(tableName string, columns, placeholders, updateParts []string) string {
	b := acquireStringBuilder()
	if len(updateParts) > 0 {
		b.WriteString("INSERT INTO ")
		b.WriteString(tableName)
		b.WriteString(" (")
		b.WriteString(StringUtilsInstance.Join(columns, ","))
		b.WriteString(") VALUES ")
		b.WriteString(StringUtilsInstance.Join(placeholders, ","))
		b.WriteString(" ON DUPLICATE KEY UPDATE ")
		b.WriteString(StringUtilsInstance.Join(updateParts, ", "))
	} else {
		b.WriteString("INSERT IGNORE INTO ")
		b.WriteString(tableName)
		b.WriteString(" (")
		b.WriteString(StringUtilsInstance.Join(columns, ","))
		b.WriteString(") VALUES ")
		b.WriteString(StringUtilsInstance.Join(placeholders, ","))
	}
	sql := b.String()
	releaseStringBuilder(b)
	return sql
}

func appendBatchInsertSQL(tableName string, columns, placeholders []string) string {
	b := acquireStringBuilder()
	b.WriteString("INSERT INTO ")
	b.WriteString(tableName)
	b.WriteString(" (")
	b.WriteString(StringUtilsInstance.Join(columns, ","))
	b.WriteString(") VALUES ")
	b.WriteString(StringUtilsInstance.Join(placeholders, ","))
	sql := b.String()
	releaseStringBuilder(b)
	return sql
}

func appendFindByIdsSQL(tableName, uidColumn string, inCount int) string {
	b := acquireStringBuilder()
	b.WriteString("SELECT * FROM ")
	b.WriteString(tableName)
	b.WriteString(" WHERE ")
	b.WriteString(uidColumn)
	b.WriteString(" IN (")
	b.WriteString(joinQuestionMarks(inCount))
	b.WriteByte(')')
	sql := b.String()
	releaseStringBuilder(b)
	return sql
}
