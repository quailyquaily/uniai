package main

import (
	"fmt"
	"strings"
)

func usageError() error {
	return fmt.Errorf("usage: speedtest --config config.yaml [--method echo|toolcalling] [--output result.csv] run [test_name]")
}

func normalizeMethod(raw string) (string, error) {
	method := strings.ToLower(strings.TrimSpace(raw))
	switch method {
	case "", methodEcho:
		return methodEcho, nil
	case methodToolCalling:
		return methodToolCalling, nil
	default:
		return "", fmt.Errorf("unsupported method %q (supported: %s, %s)", raw, methodEcho, methodToolCalling)
	}
}
