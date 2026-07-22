package db233

import "fmt"

func runEntitySerializeHook(entity IDbEntity) (err error) {
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = NewDb233Exception(fmt.Sprintf(
				"SerializeBeforeSaveDb panic: entityType=%T, panicType=%s",
				entity,
				safeValueForLog(recovered),
			))
		}
	}()
	entity.SerializeBeforeSaveDb()
	return nil
}

func runEntityDeserializeHook(entity IDbEntity) (err error) {
	if isNilStrictValue(entity) {
		return NewValidationException("实体不能为 nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = NewDb233Exception(fmt.Sprintf(
				"DeserializeAfterLoadDb panic: entityType=%T, panicType=%s",
				entity,
				safeValueForLog(recovered),
			))
		}
	}()
	entity.DeserializeAfterLoadDb()
	return nil
}
