package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr              string
	PDFToTextBinary   string
	MaxFileBytes      int64
	MaxOutputBytes    int64
	ExtractionTimeout time.Duration
	MCPMaxFileBytes   int64
	MCPAllowedRoot    string
	MCPAllowedHosts   []string
	MCPAllowedOrigins []string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

func LoadConfig() (Config, error) {
	maxFileMB, err := envPositiveInt("PDFTEXT_MAX_FILE_SIZE_MB", 50)
	if err != nil {
		return Config{}, err
	}
	maxOutputMB, err := envPositiveInt("PDFTEXT_MAX_OUTPUT_SIZE_MB", 100)
	if err != nil {
		return Config{}, err
	}
	timeoutSeconds, err := envPositiveInt("PDFTEXT_TIMEOUT_SECONDS", 120)
	if err != nil {
		return Config{}, err
	}
	mcpMaxMB, err := envPositiveInt("PDFTEXT_MCP_MAX_FILE_SIZE_MB", 2)
	if err != nil {
		return Config{}, err
	}
	if mcpMaxMB > maxFileMB {
		mcpMaxMB = maxFileMB
	}

	root := envString("PDFTEXT_MCP_ALLOWED_ROOT", "/data/input")
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve PDFTEXT_MCP_ALLOWED_ROOT: %w", err)
	}

	return Config{
		Addr:              envString("PDFTEXT_ADDR", ":8000"),
		PDFToTextBinary:   envString("PDFTEXT_BINARY", "pdftotext"),
		MaxFileBytes:      int64(maxFileMB) * 1024 * 1024,
		MaxOutputBytes:    int64(maxOutputMB) * 1024 * 1024,
		ExtractionTimeout: time.Duration(timeoutSeconds) * time.Second,
		MCPMaxFileBytes:   int64(mcpMaxMB) * 1024 * 1024,
		MCPAllowedRoot:    filepath.Clean(absRoot),
		MCPAllowedHosts: envCSV(
			"PDFTEXT_MCP_ALLOWED_HOSTS",
			[]string{"localhost", "localhost:*", "127.0.0.1", "127.0.0.1:*", "[::1]", "[::1]:*", "pdftotext", "pdftotext:*"},
		),
		MCPAllowedOrigins: envCSV(
			"PDFTEXT_MCP_ALLOWED_ORIGINS",
			[]string{"http://localhost", "http://localhost:*", "http://127.0.0.1", "http://127.0.0.1:*"},
		),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       150 * time.Second,
		WriteTimeout:      150 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, nil
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envPositiveInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func envCSV(name string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	var values []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}
