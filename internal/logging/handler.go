// Package logging provides the M0 bounded JSONL sink. It intentionally has no
// HTTP body logging, rotating files, background workers or plugin runtime.
package logging

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxRecordBytes = 4096
const MaxMessageBytes = 512

var ErrBudget = errors.New("logging byte budget exhausted")
var ErrOptions = errors.New("invalid logging options")

type Options struct {
	// Secrets are known credentials for this sink, retained only in memory.
	Secrets []string
	// MaxBytes bounds total bytes attempted by this sink; zero defaults to 1 MiB.
	MaxBytes int64
	Level    slog.Level
}
type IDs struct{ RequestID, WorkflowID, JobID string }
type contextKey struct{}

func WithIDs(ctx context.Context, ids IDs) context.Context {
	return context.WithValue(ctx, contextKey{}, ids)
}

type Stats struct {
	Written, Failed, Dropped int64
	Bytes                    int64
}
type sink struct {
	mu     sync.Mutex
	out    io.Writer
	budget int64
	stats  Stats
}
type Handler struct {
	sink    *sink
	secrets []string
	level   slog.Level
	attrs   map[string]any
	grouped bool
}

var token = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,96}$`)
var address = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
var authLine = regexp.MustCompile(`(?im)(?:cookie|set-cookie|authorization|proxy-authorization)\s*["']?\s*[:=]\s*[^\r\n]+`)
var credential = regexp.MustCompile(`(?i)(?:password|passwd|pwd|api[_-]?key|access[_-]?token|refresh[_-]?token|token|passkey|secret|session[_-]?id)\s*["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}&]+)`)
var bearer = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;"']+`)
var stringKeys = map[string]bool{"module": true, "event": true, "status": true, "error_code": true, "request_id": true, "workflow_id": true, "job_id": true, "instance_id": true, "site_id": true, "capability": true}
var numberKeys = map[string]bool{"duration_ms": true, "count": true, "http_status": true, "attempt": true}

func New(out io.Writer, options Options) (*Handler, error) {
	if out == nil || options.MaxBytes < 0 || len(options.Secrets) > 64 {
		return nil, ErrOptions
	}
	limit := options.MaxBytes
	if limit == 0 {
		limit = 1 << 20
	}
	secrets := make([]string, 0, len(options.Secrets)*3)
	for _, s := range options.Secrets {
		if len(s) > 4096 {
			return nil, ErrOptions
		}
		if s != "" {
			secrets = append(secrets, s, url.QueryEscape(s), url.PathEscape(s))
		}
	}
	// Replace longer overlapping credentials first so prefixes cannot expose suffixes.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &Handler{sink: &sink{out: out, budget: limit}, secrets: secrets, level: options.Level, attrs: map[string]any{}}, nil
}
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }
func (h *Handler) redact(s string) string {
	// Do not truncate before redaction: a partial credential could otherwise leak.
	if len(s) > 8192 {
		return "[omitted: oversized text]"
	}
	for _, secret := range h.secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	s = address.ReplaceAllString(s, "[URL REDACTED]")
	s = authLine.ReplaceAllString(s, "[AUTH REDACTED]")
	s = credential.ReplaceAllString(s, "[CREDENTIAL REDACTED]")
	s = bearer.ReplaceAllString(s, "Bearer [REDACTED]")
	// Remove control separators; JSON encoding also escapes any remaining newline.
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > MaxMessageBytes {
		return "[omitted: long text]"
	}
	return s
}
func (h *Handler) attr(a slog.Attr) (string, any, bool) {
	// Never resolve unknown LogValuers or arbitrary objects; they may expose bodies.
	if stringKeys[a.Key] && a.Value.Kind() == slog.KindString {
		s := h.redact(a.Value.String())
		if token.MatchString(s) {
			return a.Key, s, true
		}
		return "", nil, false
	}
	if numberKeys[a.Key] {
		switch a.Value.Kind() {
		case slog.KindInt64:
			n := a.Value.Int64()
			if n >= 0 && n <= 1_000_000_000_000 {
				return a.Key, n, true
			}
		case slog.KindUint64:
			n := a.Value.Uint64()
			if n <= 1_000_000_000_000 {
				return a.Key, n, true
			}
		}
	}
	if a.Key == "retryable" && a.Value.Kind() == slog.KindBool {
		return a.Key, a.Value.Bool(), true
	}
	return "", nil, false
}
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	record := map[string]any{"time": r.Time.UTC().Format(time.RFC3339Nano), "level": r.Level.String(), "message": h.redact(r.Message)}
	for k, v := range h.attrs {
		record[k] = v
	}
	if !h.grouped {
		r.Attrs(func(a slog.Attr) bool {
			if k, v, ok := h.attr(a); ok {
				record[k] = v
			}
			return true
		})
	}
	if ids, ok := ctx.Value(contextKey{}).(IDs); ok {
		for k, v := range map[string]string{"request_id": ids.RequestID, "workflow_id": ids.WorkflowID, "job_id": ids.JobID} {
			if key, value, valid := h.attr(slog.String(k, v)); valid {
				record[key] = value
			} else {
				delete(record, k)
			}
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	if len(data) > MaxRecordBytes || h.sink.stats.Bytes+int64(len(data)) > h.sink.budget {
		h.sink.stats.Dropped++
		return ErrBudget
	}
	// Account attempted bytes before write: a failing writer cannot bypass budget.
	h.sink.stats.Bytes += int64(len(data))
	n, err := h.sink.out.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		h.sink.stats.Failed++
		return err
	}
	h.sink.stats.Written++
	return nil
}
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = make(map[string]any, len(h.attrs))
	for k, v := range h.attrs {
		clone.attrs[k] = v
	}
	if !h.grouped {
		for _, a := range attrs {
			if k, v, ok := h.attr(a); ok {
				clone.attrs[k] = v
			}
		}
	}
	return &clone
}
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.grouped = true
	return &clone
}
func (h *Handler) Stats() Stats { h.sink.mu.Lock(); defer h.sink.mu.Unlock(); return h.sink.stats }
