package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// MessageHelper's original system/plugin queue is shared, destructive and
// unbounded. 500/60s limits its duplicate-key cache, never queue capacity.
// It is deliberately not persisted, broadcast, or rebuilt from error receipts.
type systemMessage struct {
	Type      string          `json:"type"`
	Title     *string         `json:"title"`
	Text      string          `json:"text"`
	Date      string          `json:"date"`
	Timestamp string          `json:"timestamp"`
	Note      json.RawMessage `json:"note"`
}
type systemMessageRuntime struct {
	mu      sync.Mutex
	queue   []json.RawMessage
	recent  map[string]time.Time
	order   []string
	stopped chan struct{}
	closed  bool
}

const systemMessageInterval = 3 * time.Second

func (m *systemMessageRuntime) done() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped == nil {
		m.stopped = make(chan struct{})
	}
	return m.stopped
}
func (m *systemMessageRuntime) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped == nil {
		m.stopped = make(chan struct{})
	}
	if !m.closed {
		m.closed = true
		close(m.stopped)
	}
	m.queue = nil
	m.recent = nil
	m.order = nil
}
func (m *systemMessageRuntime) put(role string, title *string, text string, note json.RawMessage, at time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.putLocked(role, title, text, note, at)
}
func (m *systemMessageRuntime) putLocked(role string, title *string, text string, note json.RawMessage, at time.Time) bool {
	if m.closed || (role != "system" && role != "plugin") {
		return false
	}
	label := ""
	if title != nil {
		label = *title
	}
	if role == "plugin" && label == "" {
		label = "插件通知"
		title = &label
	}
	var n any
	if len(note) != 0 && json.Unmarshal(note, &n) != nil {
		return false
	}
	switch n.(type) {
	case nil, map[string]any, []any:
	default:
		return false
	}
	normalized := n
	if n == nil {
		normalized = map[string]any{}
	}
	if values, ok := n.(map[string]any); ok && len(values) == 0 {
		normalized = map[string]any{}
	}
	if values, ok := n.([]any); ok && len(values) == 0 {
		normalized = map[string]any{}
	}
	rawKey, err := json.Marshal(struct {
		Role  string `json:"role"`
		Title string `json:"title"`
		Text  string `json:"text"`
		Note  any    `json:"note"`
		Time  string `json:"time"`
	}{role, label, text, normalized, at.Format("2006-01-02 15:04")})
	if err != nil {
		return false
	}
	key := string(rawKey)
	if m.recent == nil {
		m.recent = make(map[string]time.Time)
	}
	order := m.order[:0]
	for _, old := range m.order {
		if expiry := m.recent[old]; !expiry.After(at) {
			delete(m.recent, old)
		} else {
			order = append(order, old)
		}
	}
	m.order = order
	if expiry, ok := m.recent[key]; ok && expiry.After(at) {
		for i, old := range m.order {
			if old == key {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
		m.order = append(m.order, key)
		return false
	}
	if len(m.recent) >= 500 {
		delete(m.recent, m.order[0])
		m.order = m.order[1:]
	}
	rawNote, _ := json.Marshal(n)
	raw, err := json.Marshal(systemMessage{Type: role, Title: title, Text: text, Date: at.Format("2006-01-02 15:04:05"), Timestamp: at.Format(time.RFC3339Nano), Note: rawNote})
	if err != nil {
		return false
	}
	m.recent[key] = at.Add(time.Minute)
	m.order = append(m.order, key)
	m.queue = append(m.queue, raw)
	return true
}

// role remains an ignored compatibility argument: two tabs consume from the
// same FIFO, including plugin and system messages, not per-installation queues.
func (m *systemMessageRuntime) get(role string) json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || len(m.queue) == 0 {
		return nil
	}
	raw := m.queue[0]
	m.queue[0] = nil
	m.queue = m.queue[1:]
	return raw
}

func (a *App) systemMessageHTTP(w http.ResponseWriter, r *http.Request) {
	// GET destructively consumes shared messages. Strict cookies do not separate
	// sibling origins. Without fetch metadata, require an explicit matching
	// Origin: a cookie alone cannot distinguish native clients from old browser
	// subresources. This deliberately rejects legitimate double-missing clients.
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	if (fetchSite == "" && r.Header.Get("Origin") == "") ||
		(fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none") || !sameRequestOrigin(r) {
		a.writeError(w, r, &APIError{"ORIGIN_REJECTED", "请求来源不匹配", 403})
		return
	}
	if !a.authenticated(r) {
		a.writeError(w, r, &APIError{"UNAUTHORIZED", "请先登录", 401})
		return
	}
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	done := a.systemMessages.done()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		default:
		}
		// Session expiry/logout ends an already-open stream without taking a message.
		if !a.authenticated(r) {
			return
		}
		raw := a.systemMessages.get(r.URL.Query().Get("role"))
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return
		}
		f.Flush()
		timer := time.NewTimer(systemMessageInterval)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-done:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
