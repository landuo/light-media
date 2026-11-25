package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	LogWriteCapability = "plugin.log.write"
	LogMessageLimit    = 2048     // UTF-8 bytes, matching the host's existing message bound.
	LogPayloadLimit    = 16 << 10 // Includes JSON escaping and the finite level field.
)

type LogLevel string

const (
	LogDebug    LogLevel = "DEBUG"
	LogInfo     LogLevel = "INFO"
	LogWarning  LogLevel = "WARNING"
	LogWarn     LogLevel = "WARN" // Source-compatible warning alias.
	LogError    LogLevel = "ERROR"
	LogCritical LogLevel = "CRITICAL"
)

// LogEntryV1 deliberately has no identity, timestamp, path, caller or arbitrary
// fields. The active host call supplies attribution; a worker cannot redirect it.
type LogEntryV1 struct {
	Level   LogLevel `json:"level"`
	Message string   `json:"message"`
}

func (entry LogEntryV1) Validate() error {
	switch entry.Level {
	case LogDebug, LogInfo, LogWarning, LogWarn, LogError, LogCritical:
	default:
		return errors.New("invalid plugin log level")
	}
	if len(entry.Message) > LogMessageLimit || !utf8.ValidString(entry.Message) {
		return errors.New("invalid plugin log message")
	}
	return nil
}

// Logger belongs to one active SDK callback, just like Base. Do not retain it
// for background work. A nil error means the host accepted this log operation,
// not durable delivery: existing log-level, queue and retention policy applies.
type Logger struct{ Host HostCaller }

func (l Logger) Log(ctx context.Context, level LogLevel, message string) error {
	if l.Host == nil || ctx == nil {
		return errors.New("plugin logger requires an active host caller and context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entry := LogEntryV1{Level: level, Message: message}
	if err := entry.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = l.Host(ctx, LogWriteCapability, raw)
	return err
}
