package db233

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"
)

const defaultRecoveryMaxAttempts = 2

type recoveryDeadLetter struct {
	Component           string    `json:"component"`
	EntryID             string    `json:"entryId"`
	TableName           string    `json:"tableName,omitempty"`
	PrimaryKey          any       `json:"primaryKey,omitempty"`
	EntityTypeName      string    `json:"entityTypeName,omitempty"`
	Operation           string    `json:"operation"`
	RetryCount          int       `json:"retryCount"`
	LastError           string    `json:"lastError"`
	EntitySchemaVersion int64     `json:"entitySchemaVersion,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	TerminalAt          time.Time `json:"terminalAt"`
	Payload             any       `json:"payload"`
}

func persistRecoveryDeadLetter(rootDir string, letter recoveryDeadLetter) (string, error) {
	if rootDir == "" {
		return "", NewValidationException("死信根目录不能为空")
	}
	if letter.EntryID == "" {
		return "", NewValidationException("死信 EntryID 不能为空")
	}
	deadLetterDir := filepath.Join(rootDir, "dead-letter")
	if err := ensurePrivateRecoveryDirectory(deadLetterDir); err != nil {
		return "", fmt.Errorf("创建死信目录: %w", err)
	}
	sum := sha256.Sum256([]byte(letter.Component + "\x00" + letter.EntryID))
	fileName := letter.Component + "-" + hex.EncodeToString(sum[:16]) + ".json"
	path := filepath.Join(deadLetterDir, fileName)
	if err := writeJSONAtomic(path, letter, recoveryFileMode); err != nil {
		return "", fmt.Errorf("持久化死信: %w", err)
	}
	return path, nil
}

func normalizeRecoveryMaxAttempts(value int) (int, error) {
	if value < 0 {
		return 0, NewValidationException("RecoveryMaxAttempts 不能为负数")
	}
	if value == 0 {
		return defaultRecoveryMaxAttempts, nil
	}
	return value, nil
}
