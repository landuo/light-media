package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"time"
)

// In-memory SDK oracle fixture only. Production uses the per-effect durable controller.
// TrustedRun is created only by the embedding host after resolving a pinned
// run and current installation. This type is not a protocol payload. Concrete
// App integration must also check generation/revision/lifecycle and journal
// irreversible visits; the standalone candidate below does not replace that.
type TrustedRun struct {
	Identity       plugins.Identity
	Method, Action string
	Config         any
	Event          *EventEnvelope
	DeletedID      any
	Domain         string
	APIKeyAccepted bool
}
type MachineDependencies struct {
	Inventory func(context.Context) (Inventory, error)
	Visit     func(context.Context, Object, string, bool) (Result, error)
	Emit      func(context.Context, string, Object) error
	Notify    func(context.Context, Object) error
	Lookup    func(context.Context, string) (Object, error)
	// Checkpoint must be durable in an App adapter. The standalone SDK fixture
	// uses only owned in-memory state and makes no durability claim.
	Checkpoint func(context.Context, Object) error
	Now        func() time.Time
	Timezone   string
	RandomInt  func(int, int) (int, error)
}
type Machine struct {
	gate   chan struct{}
	engine *Engine
	deps   MachineDependencies
}

func NewMachine(deps MachineDependencies) (*Machine, error) {
	if deps.Inventory == nil || deps.Checkpoint == nil {
		return nil, errors.New("missing signin machine dependency")
	}
	if deps.Timezone == "" {
		deps.Timezone = "UTC"
	}
	e, err := NewAtZone(Inventory{}, deps.Now, deps.Timezone)
	if err != nil {
		return nil, err
	}
	return &Machine{make(chan struct{}, 1), e, deps}, nil
}
func (m *Machine) Bind(run TrustedRun) plugins.HostHandler {
	// Copy every caller-owned value before a worker starts. This is a trusted
	// binding, not an IPC bearer token whose fields could be supplied by a worker.
	run.Config = clone(run.Config)
	if run.Event != nil {
		if run.Event.Data != nil {
			run.Event = &EventEnvelope{Data: clone(run.Event.Data).(Object)}
		} else {
			run.Event = &EventEnvelope{}
		}
	}
	run.DeletedID = clone(run.DeletedID)
	return func(ctx context.Context, identity plugins.Identity, capability string, raw json.RawMessage) (json.RawMessage, error) {
		if m == nil || identity != run.Identity || identity.PluginID != "AutoSignIn" || identity.Version != Version {
			return nil, errors.New("signin identity denied")
		}
		var empty map[string]any
		if sdkDecode(raw, &empty) != nil || empty == nil || len(empty) != 0 {
			return nil, errors.New("signin arguments denied")
		}
		expected := ""
		switch run.Method {
		case "config_form":
			expected = OptionsCapability
		case "page":
			expected = SnapshotCapability
		case "http":
			expected = RunCapability
		case "invoke":
			switch run.Action {
			case "init", "reload":
				expected = ConfigureCapability
			case "service", "event":
				expected = RunCapability
			case "site_deleted":
				expected = DeleteCapability
			}
		}
		if capability != expected || expected == "" {
			return nil, errors.New("signin capability denied")
		}
		select {
		case m.gate <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-m.gate }()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inventory, err := m.deps.Inventory(ctx)
		if err != nil {
			return nil, err
		}
		inventory = publicInventory(inventory)
		m.engine.Inventory = inventory
		m.engine.Trace = []Object{}
		switch capability {
		case OptionsCapability:
			return json.Marshal(inventory)
		case SnapshotCapability:
			page, err := m.engine.Page()
			if err != nil {
				return nil, err
			}
			return json.Marshal(Object{"render_mode": "vuetify", "page": page})
		case ConfigureCapability:
			if run.Action == "reload" {
				m.engine.Stop()
				previousStop := append([]Object{}, m.engine.Trace...)
				data := m.engine.Data
				m.engine, err = NewAtZone(inventory, m.deps.Now, m.deps.Timezone)
				if err != nil {
					return nil, err
				}
				m.engine.Data = data
				m.engine.Trace = previousStop
			}
			if err = m.engine.Init(run.Config); err != nil {
				return nil, err
			}
			services, err := m.engine.Services(m.deps.RandomInt, m.deps.Timezone)
			if err != nil {
				return nil, err
			}
			if err = m.deps.Checkpoint(ctx, clone(m.engine.Snapshot()).(Object)); err != nil {
				return nil, err
			}
			return json.Marshal(Object{"state": m.engine.Enabled, "config": m.engine.Config, "services": services, "effects": m.engine.Trace, "commands": Commands()})
		case DeleteCapability:
			// MoviePilot registers events only while get_state is enabled. Direct
			// service/API calls have separate rules and do not inherit this gate.
			if Truth(m.engine.Enabled) {
				if err = m.engine.SiteDeleted(run.DeletedID); err != nil {
					return nil, err
				}
			}
		case RunCapability:
			if run.Method == "http" {
				result, err := SigninByDomain(ctx, run.Domain, DomainAPIHooks{KeyAccepted: run.APIKeyAccepted, Lookup: m.deps.Lookup, Visit: func(ctx context.Context, site Object) (Result, error) {
					if m.deps.Visit == nil {
						return Result{}, ErrAdapterNotReady
					}
					return m.deps.Visit(ctx, site, "签到", m.engine.SchemaLoaded)
				}})
				if err != nil {
					return nil, err
				}
				body, err := json.Marshal(result)
				if err != nil {
					return nil, err
				}
				return json.Marshal(plugins.HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: body})
			}
			if run.Action == "event" && !Truth(m.engine.Enabled) {
				return json.RawMessage(`null`), nil
			}
			event := run.Event
			if run.Action != "event" {
				event = nil
			}
			if err = m.engine.SignIn(ctx, event, EntryHooks{Run: RunHooks{Visit: m.deps.Visit, Emit: m.deps.Emit, Notify: m.deps.Notify}, HistoryKeys: func() []string {
				keys := []string{}
				for key := range m.engine.Data {
					keys = append(keys, key)
				}
				return keys
			}}); err != nil {
				return nil, err
			}
		}
		if err = m.deps.Checkpoint(ctx, clone(m.engine.Snapshot()).(Object)); err != nil {
			return nil, err
		}
		return json.Marshal(Object{"state": m.engine.Enabled, "config": m.engine.Config})
	}
}
