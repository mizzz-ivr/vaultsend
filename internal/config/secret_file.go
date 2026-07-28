package config

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const maxSecretFileBytes = 64 * 1024

// secretEnvOrFile は環境変数またはファイルのどちらか一方からSecretを読み込む。
// コンテナ環境では_FILE側を利用し、Secret値をプロセス環境へ直接保持しない運用を推奨する。
func secretEnvOrFile(envKey, fileEnvKey string) (string, error) {
	value := strings.TrimSpace(os.Getenv(envKey))
	filePath := strings.TrimSpace(os.Getenv(fileEnvKey))
	if value != "" && filePath != "" {
		return "", fmt.Errorf("%s and %s cannot be set at the same time", envKey, fileEnvKey)
	}
	if filePath == "" {
		return value, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileEnvKey, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxSecretFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileEnvKey, err)
	}
	if len(data) > maxSecretFileBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", fileEnvKey, maxSecretFileBytes)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("%s is empty", fileEnvKey)
	}
	return secret, nil
}
