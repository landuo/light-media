package mediaserverruntime

import (
	"context"
	"errors"
	"sync"
)

// ReloadNotice is the real manager's ModuleReload send seam. It is mandatory;
// a test may record it, but a missing production event adapter is not success.
type ReloadNotice func(context.Context) error

// Manager retains discovery order and real owner identities. There is no empty
// default registry, FNOS fallback, implicit constructor, enabled-derived vendor,
// App hook or global lifetime policy in this independent algorithm package.
type Manager struct {
	definitions []Definition
	rows        ConfigRows
	report      Report
	reloaded    ReloadNotice
	rebuildMu   sync.Mutex // serialize explicit rebuilds, not queries or owner events
	mu          sync.RWMutex
	catalog     []Definition
	running     []*Owner
}

func NewManager(definitions []Definition, rows ConfigRows, report Report, reloaded ReloadNotice) (*Manager, error) {
	if len(definitions) == 0 || rows == nil || report == nil || reloaded == nil {
		return nil, errors.New("media manager requires concrete definitions and all adapters")
	}
	seen := map[string]bool{}
	for _, d := range definitions {
		if !d.valid() || seen[d.ModuleID] {
			return nil, errors.New("invalid or unassembled concrete media module")
		}
		seen[d.ModuleID] = true
	}
	return &Manager{definitions: append([]Definition(nil), definitions...), rows: rows, report: report, reloaded: reloaded}, nil
}

func (m *Manager) Load(ctx context.Context) error {
	m.rebuildMu.Lock()
	defer m.rebuildMu.Unlock()
	return m.load(ctx)
}
func (m *Manager) load(ctx context.Context) error {
	if err := contextOK(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.catalog = []Definition{}
	m.running = []*Owner{}
	m.mu.Unlock()
	for _, definition := range m.definitions {
		if err := contextOK(ctx); err != nil {
			return err
		}
		// Source catalogs each class before constructing/initializing it. A failed
		// ordinary constructor remains discoverable but not in running modules.
		m.mu.Lock()
		m.catalog = append(m.catalog, definition)
		m.mu.Unlock()
		owner := newOwner(definition, m.rows, m.report)
		if err := owner.init(ctx); err != nil {
			if source, ok := ordinary(err); ok {
				m.report(Diagnostic{Stage: "load-error", ModuleID: definition.ModuleID, Class: source.Class})
				continue
			}
			return err
		}
		m.mu.Lock()
		m.running = append(m.running, owner)
		m.mu.Unlock()
		m.report(Diagnostic{Stage: "loaded", ModuleID: definition.ModuleID})
	}
	return nil
}
func (m *Manager) CatalogIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, len(m.catalog))
	for i, d := range m.catalog {
		ids[i] = d.ModuleID
	}
	return ids
}
func (m *Manager) Running() []*Owner {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*Owner{}, m.running...)
}
func (m *Manager) RunningModule(id string) *Owner {
	if id == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, owner := range m.running {
		if owner.ModuleID() == id {
			return owner
		}
	}
	return nil
}

// Stop preserves the running catalog and object references, including on
// ordinary stop errors. No new context, cancellation, retry or global drain is
// manufactured here. An unknown/platform/context failure is returned to caller.
func (m *Manager) Stop(ctx context.Context) error {
	if err := contextOK(ctx); err != nil {
		return err
	}
	m.report(Diagnostic{Stage: "manager-stop-begin"})
	for _, owner := range m.Running() {
		if err := owner.Stop(ctx); err != nil {
			if source, ok := ordinary(err); ok {
				m.report(Diagnostic{Stage: "stop-module-error", ModuleID: owner.ModuleID(), Class: source.Class})
				continue
			}
			return err
		}
		m.report(Diagnostic{Stage: "stopped", ModuleID: owner.ModuleID()})
	}
	m.report(Diagnostic{Stage: "manager-stop-end"})
	return nil
}
func (m *Manager) Reload(ctx context.Context) error {
	m.rebuildMu.Lock()
	defer m.rebuildMu.Unlock()
	if err := m.Stop(ctx); err != nil {
		return err
	}
	if err := m.load(ctx); err != nil {
		return err
	}
	return m.reloaded(ctx)
}
