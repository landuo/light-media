// Package plugininput implements MoviePilot's in-memory plugin input helper.
// It owns matching and consumption, not notification delivery or plugin calls.
package plugininput

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"light-media/internal/plugins"
)

type optional struct {
	value string
	set   bool
}

func opt(v *string) optional {
	if v == nil {
		return optional{}
	}
	return optional{*v, true}
}

type Key struct {
	user                  string
	channel, source, chat optional
}

func NewKey(user string, channel, source, chat *string) Key {
	c := opt(chat)
	if c.value == "" {
		c = optional{}
	}
	return Key{user, opt(channel), opt(source), c}
}
func keyOf(r plugins.PendingInputInteraction) Key {
	return NewKey(r.UserID, r.Channel, r.Source, r.ChatID)
}
func overlaps(a, b Key) bool {
	if a.user != b.user || a.chat.value != "" && b.chat.value != "" && a.chat != b.chat {
		return false
	}
	if !a.channel.set && !a.source.set || !b.channel.set && !b.source.set {
		return a.channel == b.channel && a.source == b.source
	}
	return (a.channel == b.channel || !a.channel.set || !b.channel.set) && (a.source == b.source || !a.source.set || !b.source.set)
}
func candidates(k Key) []Key {
	result := []Key{k}
	if k.source.set {
		result = append(result, Key{k.user, k.channel, optional{}, k.chat})
	}
	if k.channel.set && k.source.set {
		result = append(result, Key{k.user, optional{}, k.source, k.chat})
	}
	if !k.channel.set && !k.source.set {
		result = append(result, k)
	}
	if k.chat.set {
		k.chat = optional{}
		result = append(result, k)
		if k.source.set {
			result = append(result, Key{k.user, k.channel, optional{}, optional{}})
		}
		if k.channel.set && k.source.set {
			result = append(result, Key{k.user, optional{}, k.source, optional{}})
		}
		if !k.channel.set && !k.source.set {
			result = append(result, k)
		}
	}
	return result
}
func clone(r plugins.PendingInputInteraction) plugins.PendingInputInteraction {
	copyString := func(p *string) *string {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	r.Channel = copyString(r.Channel)
	r.Source = copyString(r.Source)
	r.Username = copyString(r.Username)
	r.ChatID = copyString(r.ChatID)
	r.PromptID = copyString(r.PromptID)
	r.PromptMessageID = copyString(r.PromptMessageID)
	r.Payload = append([]byte(nil), r.Payload...)
	return r
}
func expires(r plugins.PendingInputInteraction) time.Time {
	return r.CreatedAt.Add(time.Duration(max(1, r.TimeoutSeconds)) * time.Second)
}

type Manager struct {
	mu      sync.Mutex
	byID    map[string]plugins.PendingInputInteraction
	byKey   map[Key]string
	expired map[Key]plugins.PendingInputInteraction
}

func (m *Manager) cleanup(at time.Time) {
	for key, r := range m.expired {
		if expires(r).Add(300 * time.Second).Before(at) {
			delete(m.expired, key)
		}
	}
	for id, r := range m.byID {
		if expires(r).Before(at) {
			delete(m.byID, id)
			delete(m.byKey, keyOf(r))
			m.expired[keyOf(r)] = r
		}
	}
}
func (m *Manager) Create(input plugins.PendingInputInteraction, at time.Time) (plugins.PendingInputInteraction, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return plugins.PendingInputInteraction{}, err
	}
	input = clone(input)
	input.RequestID = hex.EncodeToString(random[:])
	input.CreatedAt = at.Round(0)
	if input.ChatID != nil && *input.ChatID == "" {
		input.ChatID = nil
	}
	if input.Channel == nil || *input.Channel != "Telegram" || input.ChatID == nil || input.PromptMessageID != nil && *input.PromptMessageID == "" {
		input.PromptMessageID = nil
	}
	if len(input.Payload) == 0 {
		input.Payload = []byte("null")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byID == nil {
		m.byID = map[string]plugins.PendingInputInteraction{}
		m.byKey = map[Key]string{}
		m.expired = map[Key]plugins.PendingInputInteraction{}
	}
	m.cleanup(at)
	key := keyOf(input)
	for old, id := range m.byKey {
		if overlaps(old, key) {
			delete(m.byID, id)
			delete(m.byKey, old)
		}
	}
	for old := range m.expired {
		if overlaps(old, key) {
			delete(m.expired, old)
		}
	}
	m.byID[input.RequestID] = input
	m.byKey[key] = input.RequestID
	return clone(input), nil
}
func (m *Manager) find(key Key) (Key, plugins.PendingInputInteraction, bool) {
	for _, candidate := range candidates(key) {
		if id, ok := m.byKey[candidate]; ok {
			r, exists := m.byID[id]
			return candidate, r, exists
		}
	}
	return Key{}, plugins.PendingInputInteraction{}, false
}
func (m *Manager) findExpired(key Key, at time.Time) (Key, plugins.PendingInputInteraction, bool) {
	for _, candidate := range candidates(key) {
		if r, ok := m.expired[candidate]; ok {
			if expires(r).Add(300 * time.Second).Before(at) {
				delete(m.expired, candidate)
				continue
			}
			return candidate, r, true
		}
	}
	return Key{}, plugins.PendingInputInteraction{}, false
}

// Lookup keeps cross-process ownership separate from matching: a worker cannot
// read or consume another plugin's session through the SDK query methods.
func (m *Manager) Lookup(key Key, owner string, pop bool, at time.Time) *plugins.PendingInputInteraction {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup(at)
	k, r, ok := m.find(key)
	if ok {
		if r.PluginID != owner {
			return nil
		}
		if pop {
			delete(m.byID, r.RequestID)
			delete(m.byKey, k)
		}
		r = clone(r)
		return &r
	}
	if pop {
		if k, r, ok := m.findExpired(key, at); ok && r.PluginID == owner {
			delete(m.expired, k)
			r = clone(r)
			return &r
		}
	}
	return nil
}
func (m *Manager) Remove(id, owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.byID[id]; ok && r.PluginID == owner {
		delete(m.byID, id)
		delete(m.byKey, keyOf(r))
	}
}
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID = nil
	m.byKey = nil
	m.expired = nil
}

// Consume matches before cleanup, including the source's special case where an
// expired, prompt-bound Telegram session disappears without an expired event.
func (m *Manager) Consume(key Key, reply *string, bypass bool, at time.Time) (*plugins.PendingInputInteraction, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k, r, ok := m.find(key); ok {
		if expires(r).Before(at) {
			delete(m.byKey, k)
			delete(m.byID, r.RequestID)
			if r.PromptMessageID != nil {
				return nil, ""
			}
			r = clone(r)
			return &r, "expired"
		}
		if r.PromptMessageID != nil && (!key.chat.set || r.ChatID == nil || key.chat.value != *r.ChatID || !bypass && (reply == nil || *reply != *r.PromptMessageID)) {
			return nil, ""
		}
		delete(m.byKey, k)
		delete(m.byID, r.RequestID)
		r = clone(r)
		return &r, "active"
	}
	m.cleanup(at)
	if k, r, ok := m.findExpired(key, at); ok {
		delete(m.expired, k)
		if r.PromptMessageID != nil {
			return nil, ""
		}
		r = clone(r)
		return &r, "expired"
	}
	m.cleanup(at)
	return nil, ""
}
