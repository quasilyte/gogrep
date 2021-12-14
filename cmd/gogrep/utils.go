package main

import (
	"os"
	"strings"
)

func isGoFilename(filename string) bool {
	return strings.HasSuffix(filename, ".go") ||
		strings.HasSuffix(filename, ".go2")
}

func envVarOrDefault(envKey, defaultValue string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultValue
}
