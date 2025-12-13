// Package log provides logging facilities for Patroni.
//
// It provides a 2-step logging approach where log messages are initially enqueued
// in-memory and later asynchronously flushed to the final destination.
package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Level represents a log level.
type Level string

const (
	LevelDebug    Level = "DEBUG"
	LevelInfo     Level = "INFO"
	LevelWarning  Level = "WARNING"
	LevelError    Level = "ERROR"
	LevelCritical Level = "CRITICAL"
)

// Format represents a log format type.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Output represents a log output destination.
type Output string

const (
	OutputStdout Output = "stdout"
	OutputStderr Output = "stderr"
)

// DefaultFormat is the default log format.
const DefaultFormat = "2006-01-02 15:04:05 INFO: %s"

// Config holds logging configuration.
type Config struct {
	Level         Level             `yaml:"level" json:"level"`
	Format        Format            `yaml:"format" json:"format"`
	Dir           string            `yaml:"dir" json:"dir"`
	Mode          int               `yaml:"mode" json:"mode"`
	MaxSize       int               `yaml:"file_size" json:"file_size"`       // megabytes
	MaxBackups    int               `yaml:"file_num" json:"file_num"`         // number of backups
	MaxAge        int               `yaml:"max_age" json:"max_age"`           // days
	Compress      bool              `yaml:"compress" json:"compress"`
	MaxQueueSize  int               `yaml:"max_queue_size" json:"max_queue_size"`
	TracebackLevel string           `yaml:"traceback_level" json:"traceback_level"`
	DateFormat    string            `yaml:"dateformat" json:"dateformat"`
	Loggers       map[string]string `yaml:"loggers" json:"loggers"`
}

// DefaultConfig returns a default logging configuration.
func DefaultConfig() *Config {
	return &Config{
		Level:         LevelInfo,
		Format:        FormatText,
		MaxSize:       25, // 25 MB
		MaxBackups:    4,
		MaxAge:        28,
		MaxQueueSize:  1000,
		TracebackLevel: "ERROR",
	}
}

// Logger is the Patroni logger with async queue support.
type Logger struct {
	mu           sync.RWMutex
	config       *Config
	logger       zerolog.Logger
	output       io.Writer
	fileWriter   *lumberjack.Logger
	queue        chan func()
	stopCh       chan struct{}
	doneCh       chan struct{}
	recordsLost  int64
	queueSize    int
}

// New creates a new Logger instance.
func New(config *Config) *Logger {
	if config == nil {
		config = DefaultConfig()
	}

	l := &Logger{
		config:      config,
		queue:       make(chan func(), config.MaxQueueSize),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}

	l.configure(config)
	return l
}

// configure applies the configuration to the logger.
func (l *Logger) configure(config *Config) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Determine output
	var output io.Writer
	if config.Dir != "" {
		// File output with rotation
		logFile := filepath.Join(config.Dir, "patroni.log")
		l.fileWriter = &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    config.MaxSize,
			MaxBackups: config.MaxBackups,
			MaxAge:     config.MaxAge,
			Compress:   config.Compress,
		}
		output = l.fileWriter

		// Apply file permissions if specified
		if config.Mode > 0 {
			os.Chmod(logFile, os.FileMode(config.Mode))
		}
	} else {
		output = os.Stderr
	}

	// Configure zerolog
	zerolog.TimeFieldFormat = time.RFC3339

	if config.Format == FormatJSON {
		l.logger = zerolog.New(output).With().Timestamp().Logger()
	} else {
		// Console format for text
		consoleWriter := zerolog.ConsoleWriter{
			Out:        output,
			TimeFormat: "2006-01-02 15:04:05",
			NoColor:    true,
		}
		l.logger = zerolog.New(consoleWriter).With().Timestamp().Logger()
	}

	// Set log level
	l.setLevel(config.Level)

	l.output = output
	l.config = config
}

// setLevel sets the global log level.
func (l *Logger) setLevel(level Level) {
	var zLevel zerolog.Level
	switch level {
	case LevelDebug:
		zLevel = zerolog.DebugLevel
	case LevelInfo:
		zLevel = zerolog.InfoLevel
	case LevelWarning:
		zLevel = zerolog.WarnLevel
	case LevelError:
		zLevel = zerolog.ErrorLevel
	case LevelCritical:
		zLevel = zerolog.FatalLevel
	default:
		zLevel = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(zLevel)
}

// Start starts the async logging thread.
func (l *Logger) Start() {
	go l.run()
}

// run is the main logging loop.
func (l *Logger) run() {
	defer close(l.doneCh)

	for {
		select {
		case <-l.stopCh:
			// Drain remaining messages
			for {
				select {
				case fn := <-l.queue:
					fn()
				default:
					return
				}
			}
		case fn := <-l.queue:
			fn()
		}
	}
}

// enqueue adds a log function to the queue.
func (l *Logger) enqueue(fn func()) {
	select {
	case l.queue <- fn:
		l.mu.Lock()
		l.queueSize++
		l.mu.Unlock()
	default:
		l.mu.Lock()
		l.recordsLost++
		l.mu.Unlock()
	}
}

// Stop stops the logger and flushes pending messages.
func (l *Logger) Stop() {
	close(l.stopCh)
	<-l.doneCh

	if l.fileWriter != nil {
		l.fileWriter.Close()
	}
}

// Shutdown is an alias for Stop.
func (l *Logger) Shutdown() {
	l.Stop()
}

// ReloadConfig reloads the logger configuration.
func (l *Logger) ReloadConfig(config *Config) {
	l.configure(config)
}

// Debug logs a debug message.
func (l *Logger) Debug(msg string, fields ...interface{}) {
	l.enqueue(func() {
		event := l.logger.Debug()
		l.addFields(event, fields...).Msg(msg)
	})
}

// Info logs an info message.
func (l *Logger) Info(msg string, fields ...interface{}) {
	l.enqueue(func() {
		event := l.logger.Info()
		l.addFields(event, fields...).Msg(msg)
	})
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string, fields ...interface{}) {
	l.enqueue(func() {
		event := l.logger.Warn()
		l.addFields(event, fields...).Msg(msg)
	})
}

// Error logs an error message.
func (l *Logger) Error(msg string, fields ...interface{}) {
	l.enqueue(func() {
		event := l.logger.Error()
		l.addFields(event, fields...).Msg(msg)
	})
}

// Fatal logs a fatal message and exits.
func (l *Logger) Fatal(msg string, fields ...interface{}) {
	event := l.logger.Fatal()
	l.addFields(event, fields...).Msg(msg)
}

// addFields adds key-value pairs to a log event.
func (l *Logger) addFields(event *zerolog.Event, fields ...interface{}) *zerolog.Event {
	for i := 0; i < len(fields)-1; i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		value := fields[i+1]
		switch v := value.(type) {
		case string:
			event = event.Str(key, v)
		case int:
			event = event.Int(key, v)
		case int64:
			event = event.Int64(key, v)
		case float64:
			event = event.Float64(key, v)
		case bool:
			event = event.Bool(key, v)
		case error:
			event = event.Err(v)
		default:
			event = event.Interface(key, v)
		}
	}
	return event
}

// QueueSize returns the current queue size.
func (l *Logger) QueueSize() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.queue)
}

// RecordsLost returns the number of lost log records.
func (l *Logger) RecordsLost() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.recordsLost
}

// Global logger instance
var globalLogger *Logger
var globalMu sync.RWMutex

// Init initializes the global logger with the given configuration.
func Init(config *Config) {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalLogger != nil {
		globalLogger.Stop()
	}

	globalLogger = New(config)
	globalLogger.Start()
}

// GetLogger returns the global logger instance.
func GetLogger() *Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalLogger
}

// Debug logs a debug message to the global logger.
func Debug(msg string, fields ...interface{}) {
	if l := GetLogger(); l != nil {
		l.Debug(msg, fields...)
	}
}

// Info logs an info message to the global logger.
func Info(msg string, fields ...interface{}) {
	if l := GetLogger(); l != nil {
		l.Info(msg, fields...)
	}
}

// Warn logs a warning message to the global logger.
func Warn(msg string, fields ...interface{}) {
	if l := GetLogger(); l != nil {
		l.Warn(msg, fields...)
	}
}

// Error logs an error message to the global logger.
func Error(msg string, fields ...interface{}) {
	if l := GetLogger(); l != nil {
		l.Error(msg, fields...)
	}
}

// Fatal logs a fatal message to the global logger.
func Fatal(msg string, fields ...interface{}) {
	if l := GetLogger(); l != nil {
		l.Fatal(msg, fields...)
	}
}

// Shutdown shuts down the global logger.
func Shutdown() {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalLogger != nil {
		globalLogger.Shutdown()
		globalLogger = nil
	}
}

// ParseLevel parses a log level string.
func ParseLevel(level string) Level {
	switch level {
	case "DEBUG", "debug":
		return LevelDebug
	case "INFO", "info":
		return LevelInfo
	case "WARNING", "warning", "WARN", "warn":
		return LevelWarning
	case "ERROR", "error":
		return LevelError
	case "CRITICAL", "critical", "FATAL", "fatal":
		return LevelCritical
	default:
		return LevelInfo
	}
}

// WithFields creates a new logger context with additional fields.
type Fields map[string]interface{}

// ContextLogger wraps a logger with additional context fields.
type ContextLogger struct {
	logger *Logger
	fields Fields
}

// With creates a new context logger with additional fields.
func With(fields Fields) *ContextLogger {
	return &ContextLogger{
		logger: GetLogger(),
		fields: fields,
	}
}

// Debug logs a debug message with context fields.
func (c *ContextLogger) Debug(msg string, additionalFields ...interface{}) {
	fields := c.mergeFields(additionalFields)
	c.logger.Debug(msg, fields...)
}

// Info logs an info message with context fields.
func (c *ContextLogger) Info(msg string, additionalFields ...interface{}) {
	fields := c.mergeFields(additionalFields)
	c.logger.Info(msg, fields...)
}

// Warn logs a warning message with context fields.
func (c *ContextLogger) Warn(msg string, additionalFields ...interface{}) {
	fields := c.mergeFields(additionalFields)
	c.logger.Warn(msg, fields...)
}

// Error logs an error message with context fields.
func (c *ContextLogger) Error(msg string, additionalFields ...interface{}) {
	fields := c.mergeFields(additionalFields)
	c.logger.Error(msg, fields...)
}

func (c *ContextLogger) mergeFields(additionalFields []interface{}) []interface{} {
	var result []interface{}
	for k, v := range c.fields {
		result = append(result, k, v)
	}
	result = append(result, additionalFields...)
	return result
}

// FileHandler wraps a file with rotation support.
type FileHandler struct {
	mu         sync.Mutex
	logger     *lumberjack.Logger
	mode       os.FileMode
}

// NewFileHandler creates a new file handler with rotation.
func NewFileHandler(filename string, mode os.FileMode, maxSize, maxBackups, maxAge int) *FileHandler {
	return &FileHandler{
		logger: &lumberjack.Logger{
			Filename:   filename,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
		},
		mode: mode,
	}
}

// Write implements io.Writer.
func (h *FileHandler) Write(p []byte) (n int, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.logger.Write(p)
}

// Close closes the file handler.
func (h *FileHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.logger.Close()
}

// Rotate forces log rotation.
func (h *FileHandler) Rotate() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.logger.Rotate()
}

// Timestamp formats for logging.
const (
	TimestampISO8601 = "2006-01-02T15:04:05.000Z07:00"
	TimestampRFC3339 = time.RFC3339
	TimestampUnix    = "unix"
)

// FormatTimestamp formats a timestamp according to the specified format.
func FormatTimestamp(t time.Time, format string) string {
	if format == TimestampUnix {
		return fmt.Sprintf("%d", t.Unix())
	}
	return t.Format(format)
}
