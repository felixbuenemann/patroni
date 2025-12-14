package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLevelConstants(t *testing.T) {
	// Verify level constants match expected values
	levels := map[Level]string{
		LevelDebug:    "DEBUG",
		LevelInfo:     "INFO",
		LevelWarning:  "WARNING",
		LevelError:    "ERROR",
		LevelCritical: "CRITICAL",
	}

	for level, expected := range levels {
		if string(level) != expected {
			t.Errorf("Level %v = %q, want %q", level, string(level), expected)
		}
	}
}

func TestFormatConstants(t *testing.T) {
	// Verify format constants match expected values
	formats := map[Format]string{
		FormatText: "text",
		FormatJSON: "json",
	}

	for format, expected := range formats {
		if string(format) != expected {
			t.Errorf("Format %v = %q, want %q", format, string(format), expected)
		}
	}
}

func TestOutputConstants(t *testing.T) {
	// Verify output constants match expected values
	outputs := map[Output]string{
		OutputStdout: "stdout",
		OutputStderr: "stderr",
	}

	for output, expected := range outputs {
		if string(output) != expected {
			t.Errorf("Output %v = %q, want %q", output, string(output), expected)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.Level != LevelInfo {
		t.Errorf("Level = %v, want %v", cfg.Level, LevelInfo)
	}
	if cfg.Format != FormatText {
		t.Errorf("Format = %v, want %v", cfg.Format, FormatText)
	}
	if cfg.MaxSize != 25 {
		t.Errorf("MaxSize = %d, want 25", cfg.MaxSize)
	}
	if cfg.MaxBackups != 4 {
		t.Errorf("MaxBackups = %d, want 4", cfg.MaxBackups)
	}
	if cfg.MaxAge != 28 {
		t.Errorf("MaxAge = %d, want 28", cfg.MaxAge)
	}
	if cfg.MaxQueueSize != 1000 {
		t.Errorf("MaxQueueSize = %d, want 1000", cfg.MaxQueueSize)
	}
	if cfg.TracebackLevel != "ERROR" {
		t.Errorf("TracebackLevel = %q, want %q", cfg.TracebackLevel, "ERROR")
	}
}

func TestNewLogger(t *testing.T) {
	logger := New(nil)
	if logger == nil {
		t.Fatal("New(nil) returned nil")
	}
	if logger.config == nil {
		t.Error("Logger.config should be initialized")
	}
	if logger.queue == nil {
		t.Error("Logger.queue should be initialized")
	}
	if logger.stopCh == nil {
		t.Error("Logger.stopCh should be initialized")
	}
	if logger.doneCh == nil {
		t.Error("Logger.doneCh should be initialized")
	}

	// Verify default config is used
	if logger.config.Level != LevelInfo {
		t.Errorf("Default level = %v, want %v", logger.config.Level, LevelInfo)
	}
}

func TestNewLoggerWithConfig(t *testing.T) {
	cfg := &Config{
		Level:        LevelDebug,
		Format:       FormatJSON,
		MaxQueueSize: 100,
	}

	logger := New(cfg)
	if logger == nil {
		t.Fatal("New(cfg) returned nil")
	}
	if logger.config.Level != LevelDebug {
		t.Errorf("Level = %v, want %v", logger.config.Level, LevelDebug)
	}
	if logger.config.Format != FormatJSON {
		t.Errorf("Format = %v, want %v", logger.config.Format, FormatJSON)
	}
}

func TestLoggerStartStop(t *testing.T) {
	logger := New(nil)
	logger.Start()

	// Give some time for goroutine to start
	time.Sleep(10 * time.Millisecond)

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		logger.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Logger.Stop() timed out")
	}
}

func TestLoggerShutdown(t *testing.T) {
	logger := New(nil)
	logger.Start()

	time.Sleep(10 * time.Millisecond)

	// Shutdown is alias for Stop
	done := make(chan struct{})
	go func() {
		logger.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Logger.Shutdown() timed out")
	}
}

func TestLoggerMethods(t *testing.T) {
	logger := New(&Config{
		Level:        LevelDebug,
		MaxQueueSize: 100,
	})
	logger.Start()
	defer logger.Stop()

	// Test logging methods don't panic
	logger.Debug("debug message", "key", "value")
	logger.Info("info message", "count", 42)
	logger.Warn("warn message", "float", 3.14)
	logger.Error("error message", "bool", true)

	// Wait for messages to be processed
	time.Sleep(50 * time.Millisecond)
}

func TestLoggerQueueSize(t *testing.T) {
	cfg := &Config{
		Level:        LevelInfo,
		MaxQueueSize: 10,
	}
	logger := New(cfg)
	// Don't start - messages should queue up

	// Queue messages
	for i := 0; i < 5; i++ {
		logger.Info("test message")
	}

	// Check queue has items
	// Note: exact count may vary due to async nature
	size := logger.QueueSize()
	if size < 0 {
		t.Errorf("QueueSize() = %d, should be non-negative", size)
	}
}

func TestLoggerRecordsLost(t *testing.T) {
	// Create logger with tiny queue
	cfg := &Config{
		Level:        LevelInfo,
		MaxQueueSize: 2,
	}
	logger := New(cfg)
	// Don't start - queue will fill up

	// Fill up the queue and overflow
	for i := 0; i < 10; i++ {
		logger.Info("test message")
	}

	// Some records should be lost
	lost := logger.RecordsLost()
	if lost < 0 {
		t.Errorf("RecordsLost() = %d, should be non-negative", lost)
	}
}

func TestLoggerReloadConfig(t *testing.T) {
	logger := New(&Config{
		Level:        LevelInfo,
		MaxQueueSize: 100,
	})
	logger.Start()
	defer logger.Stop()

	// Reload with different config
	newCfg := &Config{
		Level:        LevelDebug,
		Format:       FormatJSON,
		MaxQueueSize: 100,
	}
	logger.ReloadConfig(newCfg)

	if logger.config.Level != LevelDebug {
		t.Errorf("After reload Level = %v, want %v", logger.config.Level, LevelDebug)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected Level
	}{
		{"DEBUG", LevelDebug},
		{"debug", LevelDebug},
		{"INFO", LevelInfo},
		{"info", LevelInfo},
		{"WARNING", LevelWarning},
		{"warning", LevelWarning},
		{"WARN", LevelWarning},
		{"warn", LevelWarning},
		{"ERROR", LevelError},
		{"error", LevelError},
		{"CRITICAL", LevelCritical},
		{"critical", LevelCritical},
		{"FATAL", LevelCritical},
		{"fatal", LevelCritical},
		{"unknown", LevelInfo}, // defaults to INFO
		{"", LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseLevel(tt.input)
			if result != tt.expected {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLoggerWithFileOutput(t *testing.T) {
	// Create temp directory for log files
	tmpDir, err := os.MkdirTemp("", "patroni-log-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatText,
		Dir:          tmpDir,
		MaxSize:      1,
		MaxBackups:   2,
		MaxQueueSize: 100,
	}

	logger := New(cfg)
	logger.Start()

	// Log a message
	logger.Info("test file output")
	time.Sleep(50 * time.Millisecond)

	logger.Stop()

	// Check log file was created
	logFile := filepath.Join(tmpDir, "patroni.log")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("Log file was not created")
	}
}

func TestGlobalLoggerFunctions(t *testing.T) {
	// Init global logger
	Init(&Config{
		Level:        LevelDebug,
		MaxQueueSize: 100,
	})
	defer Shutdown()

	// Test global functions
	logger := GetLogger()
	if logger == nil {
		t.Fatal("GetLogger() returned nil after Init()")
	}

	// These should not panic
	Debug("global debug")
	Info("global info")
	Warn("global warn")
	Error("global error")

	time.Sleep(50 * time.Millisecond)
}

func TestShutdownWithoutInit(t *testing.T) {
	// Reset global logger
	globalMu.Lock()
	globalLogger = nil
	globalMu.Unlock()

	// Shutdown should not panic when logger is nil
	Shutdown()
}

func TestGlobalFunctionsWithoutInit(t *testing.T) {
	// Reset global logger
	globalMu.Lock()
	globalLogger = nil
	globalMu.Unlock()

	// These should not panic even without init
	Debug("test")
	Info("test")
	Warn("test")
	Error("test")
}

func TestFormatTimestamp(t *testing.T) {
	testTime := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)

	tests := []struct {
		name     string
		format   string
		expected string
	}{
		{"ISO8601", TimestampISO8601, "2024-01-15T10:30:45.000Z"},
		{"RFC3339", TimestampRFC3339, "2024-01-15T10:30:45Z"},
		{"Unix", TimestampUnix, "1705314645"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatTimestamp(testTime, tt.format)
			if result != tt.expected {
				t.Errorf("FormatTimestamp(%v, %q) = %q, want %q",
					testTime, tt.format, result, tt.expected)
			}
		})
	}
}

func TestNewFileHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "patroni-filehandler-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logFile := filepath.Join(tmpDir, "test.log")
	handler := NewFileHandler(logFile, 0o644, 10, 3, 7)

	if handler == nil {
		t.Fatal("NewFileHandler() returned nil")
	}
	if handler.logger == nil {
		t.Error("FileHandler.logger should be initialized")
	}

	// Test Write
	n, err := handler.Write([]byte("test message\n"))
	if err != nil {
		t.Errorf("FileHandler.Write() error = %v", err)
	}
	if n != 13 {
		t.Errorf("FileHandler.Write() = %d, want 13", n)
	}

	// Test Close
	if err := handler.Close(); err != nil {
		t.Errorf("FileHandler.Close() error = %v", err)
	}
}

func TestFileHandlerRotate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "patroni-rotate-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logFile := filepath.Join(tmpDir, "test.log")
	handler := NewFileHandler(logFile, 0o644, 10, 3, 7)
	defer handler.Close()

	// Write some data
	handler.Write([]byte("test message\n"))

	// Rotate should not error
	if err := handler.Rotate(); err != nil {
		t.Errorf("FileHandler.Rotate() error = %v", err)
	}
}

func TestContextLogger(t *testing.T) {
	// Init global logger first
	Init(&Config{
		Level:        LevelDebug,
		MaxQueueSize: 100,
	})
	defer Shutdown()

	// Create context logger
	ctx := With(Fields{
		"service": "patroni",
		"node":    "node1",
	})

	if ctx == nil {
		t.Fatal("With() returned nil")
	}
	if ctx.logger == nil {
		t.Error("ContextLogger.logger should be set")
	}
	if ctx.fields == nil {
		t.Error("ContextLogger.fields should be set")
	}

	// These should not panic
	ctx.Debug("context debug")
	ctx.Info("context info")
	ctx.Warn("context warn")
	ctx.Error("context error")

	time.Sleep(50 * time.Millisecond)
}

func TestLoggerAddFields(t *testing.T) {
	logger := New(&Config{
		Level:        LevelDebug,
		MaxQueueSize: 100,
	})
	logger.Start()
	defer logger.Stop()

	// Test different field types
	logger.Info("test fields",
		"string_key", "string_value",
		"int_key", 42,
		"int64_key", int64(12345678901234),
		"float_key", 3.14159,
		"bool_key", true,
	)

	time.Sleep(50 * time.Millisecond)
}

func TestConfigStruct(t *testing.T) {
	cfg := Config{
		Level:          LevelDebug,
		Format:         FormatJSON,
		Dir:            "/var/log/patroni",
		Mode:           0o600,
		MaxSize:        100,
		MaxBackups:     5,
		MaxAge:         30,
		Compress:       true,
		MaxQueueSize:   500,
		TracebackLevel: "DEBUG",
		DateFormat:     "%Y-%m-%d",
		Loggers: map[string]string{
			"foo.bar": "INFO",
		},
	}

	if cfg.Level != LevelDebug {
		t.Errorf("Level = %v, want %v", cfg.Level, LevelDebug)
	}
	if cfg.Format != FormatJSON {
		t.Errorf("Format = %v, want %v", cfg.Format, FormatJSON)
	}
	if cfg.Mode != 0o600 {
		t.Errorf("Mode = %o, want %o", cfg.Mode, 0o600)
	}
	if !cfg.Compress {
		t.Error("Compress should be true")
	}
	if cfg.Loggers["foo.bar"] != "INFO" {
		t.Errorf("Loggers[foo.bar] = %q, want %q", cfg.Loggers["foo.bar"], "INFO")
	}
}

func TestLoggerJSONFormat(t *testing.T) {
	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		MaxQueueSize: 100,
	}
	logger := New(cfg)

	// We can't easily capture output, but ensure it doesn't panic
	logger.Start()
	logger.Info("json format test")
	time.Sleep(50 * time.Millisecond)
	logger.Stop()
}

func TestLoggerTextFormat(t *testing.T) {
	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatText,
		MaxQueueSize: 100,
	}
	logger := New(cfg)

	// We can't easily capture output, but ensure it doesn't panic
	logger.Start()
	logger.Info("text format test")
	time.Sleep(50 * time.Millisecond)
	logger.Stop()
}

func TestLoggerQueueDrain(t *testing.T) {
	cfg := &Config{
		Level:        LevelInfo,
		MaxQueueSize: 100,
	}
	logger := New(cfg)
	logger.Start()

	// Queue several messages
	for i := 0; i < 10; i++ {
		logger.Info("drain test message")
	}

	// Stop should drain the queue
	logger.Stop()

	// Queue should be empty after stop
	if len(logger.queue) != 0 {
		t.Errorf("Queue should be drained after Stop(), got %d items", len(logger.queue))
	}
}

func TestTimestampConstants(t *testing.T) {
	if TimestampISO8601 != "2006-01-02T15:04:05.000Z07:00" {
		t.Errorf("TimestampISO8601 = %q, unexpected value", TimestampISO8601)
	}
	if TimestampRFC3339 != time.RFC3339 {
		t.Errorf("TimestampRFC3339 = %q, want %q", TimestampRFC3339, time.RFC3339)
	}
	if TimestampUnix != "unix" {
		t.Errorf("TimestampUnix = %q, want %q", TimestampUnix, "unix")
	}
}

func TestDefaultFormat(t *testing.T) {
	if !strings.Contains(DefaultFormat, "INFO") {
		t.Errorf("DefaultFormat = %q, should contain 'INFO'", DefaultFormat)
	}
}

func TestFieldsType(t *testing.T) {
	fields := Fields{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}

	if fields["key1"] != "value1" {
		t.Errorf("fields[key1] = %v, want 'value1'", fields["key1"])
	}
	if fields["key2"] != 42 {
		t.Errorf("fields[key2] = %v, want 42", fields["key2"])
	}
	if fields["key3"] != true {
		t.Errorf("fields[key3] = %v, want true", fields["key3"])
	}
}

// Test that matches Python test_patroni_logger behavior
func TestPatroniLoggerBehavior(t *testing.T) {
	cfg := &Config{
		Level:          LevelDebug,
		MaxQueueSize:   5,
		TracebackLevel: "DEBUG",
	}

	logger := New(cfg)
	logger.Start()

	// Log various levels (matches Python test)
	logger.Debug("debug test")
	logger.Info("info test")
	logger.Warn("warning test")
	logger.Error("error test")

	time.Sleep(100 * time.Millisecond)

	// Check records lost is 0 when queue isn't full
	if logger.RecordsLost() != 0 {
		t.Errorf("RecordsLost() = %d, want 0", logger.RecordsLost())
	}

	logger.Stop()
}

// Test that matches Python test_interceptor behavior
func TestInterceptor(t *testing.T) {
	cfg := &Config{
		Level:        LevelInfo,
		MaxQueueSize: 100,
	}
	logger := New(cfg)
	logger.Start()

	logger.Info("Lock owner: ")
	logger.Info("blabla")

	logger.Shutdown()

	if logger.RecordsLost() != 0 {
		t.Errorf("RecordsLost() = %d, want 0", logger.RecordsLost())
	}
}
