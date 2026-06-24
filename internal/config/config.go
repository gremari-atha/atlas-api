package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadEnv reads a .env file and sets the environment variables.
// ponytail: custom lightweight loader to avoid joho/godotenv dependency
func LoadEnv(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return // Ignore missing .env, rely on system env
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`) // strip optional quotes
			os.Setenv(key, val)
		}
	}
}

// GetEnvAsDuration retrieves a duration in seconds from an environment variable.
// If the variable is empty or not a valid integer, it returns the default value.
func GetEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return time.Duration(val) * time.Second
}

// GetEnvAsDurationMs retrieves a duration in milliseconds from an environment variable.
// If the variable is empty or not a valid integer, it returns the default value.
func GetEnvAsDurationMs(key string, defaultVal time.Duration) time.Duration {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return time.Duration(val) * time.Millisecond
}

