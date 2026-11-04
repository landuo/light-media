// Package mediaserverruntime owns the source media-module instance lifecycle.
// It is not a vendor registry: callers must supply concrete vendor factories.
package mediaserverruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	"light-media/internal/plugins"
)

// Vendor is a real long-lived provider object. IsInactive belongs to the later
// service consumer; configuration/enabled alone must never implement its state.
// The lifecycle core preserves this exact reference and does not call it here.
type Vendor interface {
	IsInactive(context.Context) (bool, error)
}
type PlexVendor interface {
	Vendor
	Close(context.Context) error
}
type AuthenticatedVendor interface {
	Vendor
	IsAuthenticated(context.Context) (bool, error)
	Disconnect(context.Context) error
}

// Factory must perform the concrete vendor constructor, including its original
// kwargs/config + separate sync_libraries semantics. It must not synthesize an
// authenticated object from configuration. Source constructor early-return still
// returns its concrete non-nil object. An unimplemented factory is a hard error.
type Factory func(context.Context, plugins.MediaServerConfig) (Vendor, error)

// ConfigRows returns validated ordered rows BEFORE any name projection. Neither
// ParseMediaServerConfigs(...,true) nor (...,false) satisfies this seam. A nil or
// empty list means empty rows, including source falsey raw configuration. This
// six-module path always returns a dict; generic ServiceBase configs=None is not
// reachable through _MediaServerBase and is not fabricated here.
type ConfigRows func(context.Context) ([]plugins.MediaServerConfigEntry, error)

type Diagnostic struct{ Stage, ModuleID, Class string }
type Report func(Diagnostic)

// SourceError is an explicit ordinary Python Exception mapping made by the
// concrete adapter, never an automatic wrapper around every Go error. Context,
// ownership/current-generation/credential/integrity/policy errors must stay
// outside it. No error text/config is exposed through Diagnostic.
type SourceError struct {
	Class string
	Cause error
}

func (e *SourceError) Error() string { return "media vendor source exception: " + e.Class }
func (e *SourceError) Unwrap() error { return e.Cause }
func ordinary(err error) (*SourceError, bool) {
	// Deliberately do not unwrap errors.Join or unknown outer errors into a
	// swallowable Exception; their other failures must not disappear.
	e, ok := err.(*SourceError)
	return e, ok && e != nil && e.Class != "" && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// Definition is one discovered concrete media module; order is caller-supplied
// discovery order, never priority-sorted. The six admitted class/type pairs are
// fixed original declarations, not arbitrary system-management modules.
type Definition struct {
	ModuleID, ServiceName string
	Factory               Factory
}

func (d Definition) valid() bool {
	names := map[string]string{"EmbyModule": "emby", "JellyfinModule": "jellyfin", "PlexModule": "plex", "TrimeMediaModule": "trimemedia", "UgreenModule": "ugreen", "ZSpaceModule": "zspace"}
	return d.Factory != nil && names[d.ModuleID] == d.ServiceName && d.ServiceName != ""
}

type Instance struct {
	Key    json.RawMessage
	Vendor Vendor
}
type State struct {
	ServiceName                              string
	ConfigsInitialized, InstancesInitialized bool
	Configs                                  []plugins.MediaServerConfigEntry
	Instances                                []Instance
}

// Owner corresponds to one actual module object, not one named service. State
// publication uses short locks: queries can observe the empty/new prefix during
// construction, exactly not an atomic-success swap or rollback to old objects.
type Owner struct {
	definition Definition
	rows       ConfigRows
	report     Report
	reloadMu   sync.Mutex
	mu         sync.RWMutex
	state      State
}

func newOwner(d Definition, rows ConfigRows, report Report) *Owner {
	return &Owner{definition: d, rows: rows, report: report}
}
func (o *Owner) ModuleID() string               { return o.definition.ModuleID }
func copyRaw(v json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), v...) }
func cloneConfig(c plugins.MediaServerConfig) plugins.MediaServerConfig {
	c.Name = copyRaw(c.Name)
	c.Type = copyRaw(c.Type)
	c.Config = copyRaw(c.Config)
	c.SyncLibraries = copyRaw(c.SyncLibraries)
	c.SyncInterval = copyRaw(c.SyncInterval)
	if c.Enabled != nil {
		v := *c.Enabled
		c.Enabled = &v
	}
	return c
}
func cloneEntry(e plugins.MediaServerConfigEntry) plugins.MediaServerConfigEntry {
	return plugins.MediaServerConfigEntry{Key: copyRaw(e.Key), Value: cloneConfig(e.Value), FieldsSet: append([]string(nil), e.FieldsSet...)}
}
func canonicalName(raw json.RawMessage) (json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte(`null`)) {
		return json.RawMessage(`null`), nil
	}
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return nil, err
	}
	return plugins.EncodeSystemConfigJSONString(points)
}
func equalType(raw json.RawMessage, want string) (bool, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte(`null`)) {
		return false, nil
	}
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return false, err
	}
	return string(points) == want, nil
}
func selectConfigs(rows []plugins.MediaServerConfigEntry, service string) ([]plugins.MediaServerConfigEntry, error) {
	out := []plugins.MediaServerConfigEntry{}
	positions := map[string]int{}
	for _, row := range rows {
		matched, err := equalType(row.Value.Type, service)
		if err != nil {
			return nil, err
		}
		if !matched || row.Value.Enabled == nil || !*row.Value.Enabled {
			continue
		}
		key, err := canonicalName(row.Value.Name)
		if err != nil {
			return nil, err
		}
		entry := cloneEntry(row)
		entry.Key = key
		if position, ok := positions[string(key)]; ok {
			out[position] = entry
		} else {
			positions[string(key)] = len(out)
			out = append(out, entry)
		}
	}
	return out, nil
}

func contextOK(ctx context.Context) error {
	if ctx == nil {
		return errors.New("media lifecycle requires a context")
	}
	return ctx.Err()
}
func (o *Owner) init(ctx context.Context) error {
	if err := contextOK(ctx); err != nil {
		return err
	}
	o.mu.Lock()
	o.state.ServiceName = o.definition.ServiceName
	o.mu.Unlock()
	rows, err := o.rows(ctx)
	if err != nil {
		return err
	}
	configs, err := selectConfigs(rows, o.definition.ServiceName)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.state.Configs = configs
	o.state.ConfigsInitialized = true
	o.state.Instances = []Instance{}
	o.state.InstancesInitialized = true
	o.mu.Unlock()
	for _, entry := range configs {
		if err := contextOK(ctx); err != nil {
			return err
		}
		vendor, err := o.definition.Factory(ctx, cloneConfig(entry.Value))
		if err != nil {
			return err
		}
		if absentVendor(vendor) {
			return errors.New("concrete media factory returned no object")
		}
		// Validate only this module's actual stop interface. These are integration
		// errors, not source constructor exceptions or silently absent services.
		switch o.definition.ModuleID {
		case "PlexModule":
			if _, ok := vendor.(PlexVendor); !ok {
				return errors.New("Plex factory lacks concrete close operation")
			}
		case "TrimeMediaModule", "UgreenModule":
			if _, ok := vendor.(AuthenticatedVendor); !ok {
				return errors.New("media factory lacks concrete authentication/disconnect operations")
			}
		}
		o.mu.Lock()
		o.state.Instances = append(o.state.Instances, Instance{Key: copyRaw(entry.Key), Vendor: vendor})
		o.mu.Unlock()
	}
	return nil
}

// An interface carrying a nil concrete pointer is not a constructed vendor.
func absentVendor(v Vendor) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}

func (o *Owner) Snapshot() State {
	o.mu.RLock()
	defer o.mu.RUnlock()
	s := o.state
	s.Configs = make([]plugins.MediaServerConfigEntry, len(o.state.Configs))
	for i, c := range o.state.Configs {
		s.Configs[i] = cloneEntry(c)
	}
	s.Instances = make([]Instance, len(o.state.Instances))
	for i, v := range o.state.Instances {
		s.Instances[i] = Instance{Key: copyRaw(v.Key), Vendor: v.Vendor}
	}
	return s
}
func (o *Owner) Instances() []Instance { return o.Snapshot().Instances }
func (o *Owner) DefaultConfigName() json.RawMessage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(o.state.Configs) == 0 {
		return nil
	}
	return copyRaw(o.state.Configs[0].Value.Name)
}
func nameTruthy(raw json.RawMessage) bool {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	return err == nil && len(points) != 0
}
func (o *Owner) GetInstance(name json.RawMessage) (Vendor, error) {
	if len(name) != 0 {
		if _, err := canonicalName(name); err != nil {
			return nil, err
		}
	}
	s := o.Snapshot()
	if len(s.Instances) == 0 {
		return nil, nil
	}
	if !nameTruthy(name) {
		if len(s.Configs) == 0 || !nameTruthy(s.Configs[0].Value.Name) {
			return nil, nil
		}
		name = s.Configs[0].Value.Name
	}
	key, err := canonicalName(name)
	if err != nil {
		return nil, err
	}
	for _, v := range s.Instances {
		if bytes.Equal(v.Key, key) {
			return v.Vendor, nil
		}
	}
	return nil, nil
}
func (o *Owner) GetConfig(name json.RawMessage) (*plugins.MediaServerConfig, error) {
	if len(name) != 0 {
		if _, err := canonicalName(name); err != nil {
			return nil, err
		}
	}
	s := o.Snapshot()
	if len(s.Configs) == 0 {
		return nil, nil
	}
	if !nameTruthy(name) {
		if !nameTruthy(s.Configs[0].Value.Name) {
			return nil, nil
		}
		name = s.Configs[0].Value.Name
	}
	key, err := canonicalName(name)
	if err != nil {
		return nil, err
	}
	for _, v := range s.Configs {
		if bytes.Equal(v.Key, key) {
			c := cloneConfig(v.Value)
			return &c, nil
		}
	}
	return nil, nil
}

// Stop is the original module stop, not terminal disposal or global shutdown.
// It does not clear state, remove the owner, or take the reload lock. Concrete
// providers own the distinct Plex close / Trime logout / Ugreen non-logout IO.
func (o *Owner) Stop(ctx context.Context) error {
	if err := contextOK(ctx); err != nil {
		return err
	}
	if o.definition.ModuleID != "PlexModule" && o.definition.ModuleID != "TrimeMediaModule" && o.definition.ModuleID != "UgreenModule" {
		return nil
	}
	for _, v := range o.Instances() {
		if err := contextOK(ctx); err != nil {
			return err
		}
		var err error
		if o.definition.ModuleID == "PlexModule" {
			err = v.Vendor.(PlexVendor).Close(ctx)
		} else {
			provider := v.Vendor.(AuthenticatedVendor)
			var authenticated bool
			authenticated, err = provider.IsAuthenticated(ctx)
			if err == nil && authenticated {
				err = provider.Disconnect(ctx)
			}
		}
		if err != nil {
			if source, ok := ordinary(err); ok {
				o.report(Diagnostic{Stage: "stop-instance-error", ModuleID: o.ModuleID(), Class: source.Class})
				continue
			}
			return err
		}
	}
	return nil
}

// HandleConfigChanged is the concrete class callback after current-owner
// lookup. Event value intentionally is not accepted: initialization re-reads
// current private rows. This call is synchronous; no event bus is impersonated.
func (o *Owner) HandleConfigChanged(ctx context.Context, present bool, keys []string) error {
	if err := contextOK(ctx); err != nil {
		return err
	}
	matched := false
	if present {
		for _, key := range keys {
			if key == "MediaServers" {
				matched = true
				break
			}
		}
	}
	if !matched {
		return nil
	}
	o.report(Diagnostic{Stage: "reload-begin", ModuleID: o.ModuleID()})
	o.reloadMu.Lock()
	defer o.reloadMu.Unlock()
	if err := contextOK(ctx); err != nil {
		return err
	}
	if err := o.Stop(ctx); err != nil {
		if source, ok := ordinary(err); ok {
			o.report(Diagnostic{Stage: "stop-owner-error", ModuleID: o.ModuleID(), Class: source.Class})
		} else {
			return err
		}
	}
	return o.init(ctx) // Ordinary init error escapes; new prefix is retained.
}
