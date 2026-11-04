package mediaserverruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

// ServiceInfo is an in-process reference to an existing running owner and its
// concrete vendor. It is not a worker DTO or a serialized object capability.
// Config is the current helper's name-joined config, which can be absent or
// belong to a different type than the owner when names collide across modules.
type ServiceInfo struct {
	Name            json.RawMessage
	Instance        Vendor `json:"-"`
	Module          *Owner `json:"-"`
	Type            json.RawMessage
	Config          *plugins.MediaServerConfig
	ConfigFieldsSet []string
}

// Services implements only the original ServiceBaseHelper instance queries.
// It never constructs a vendor, infers its state from enabled, or caches an
// owner snapshot across calls. Manager assembly/Load and private raw ownership
// remain the host's responsibility; missing adapters are not an empty registry.
type Services struct {
	manager *Manager
	read    RawConfig
	report  ConfigReport
}

func NewServices(manager *Manager, read RawConfig, report ConfigReport) (*Services, error) {
	if manager == nil || read == nil || report == nil {
		return nil, errors.New("media services require an assembled manager and private configuration adapters")
	}
	return &Services{manager: manager, read: read, report: report}, nil
}

func (s *Services) configs(ctx context.Context) ([]plugins.MediaServerConfigEntry, error) {
	if err := contextOK(ctx); err != nil {
		return nil, err
	}
	raw, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	if err := contextOK(ctx); err != nil {
		return nil, err
	}
	// Deliberately the helper's globally name-projected config dictionary,
	// NOT the unprojected rows used during each module's construction.
	parsed, err := plugins.ParseMediaServerConfigs(raw, false)
	for _, diagnostic := range parsed.Diagnostics {
		s.report(diagnostic)
	}
	if err != nil {
		if source, ok := err.(*plugins.MediaServerConfigSourceError); ok && source != nil {
			switch source.Class {
			case "TypeError", "OverflowError":
				return nil, &SourceError{Class: source.Class, Cause: source}
			}
		}
		return nil, err
	}
	return parsed.Entries, nil
}

// visit preserves generator short-circuiting: GetService need not inspect
// later owners after finding the first matching concrete instance. No vendor
// method (including IsInactive) is called by these three original queries.
func (s *Services) visit(ctx context.Context, yield func(ServiceInfo) bool) error {
	configs, err := s.configs(ctx)
	if err != nil {
		return err
	}
	byName := make(map[string]*plugins.MediaServerConfigEntry, len(configs))
	for i := range configs {
		byName[string(configs[i].Key)] = &configs[i]
	}
	for _, owner := range s.manager.Running() {
		if err := contextOK(ctx); err != nil {
			return err
		}
		for _, entry := range owner.Instances() {
			if err := contextOK(ctx); err != nil {
				return err
			}
			if absentVendor(entry.Vendor) {
				continue
			}
			info := ServiceInfo{Name: copyRaw(entry.Key), Instance: entry.Vendor, Module: owner, Type: json.RawMessage(`null`)}
			if config, ok := byName[string(entry.Key)]; ok {
				// Models are newly owned by this query, not Store/Owner state.
				// Repeated names in one iterator share the one helper model.
				info.Config = &config.Value
				info.Type = copyRaw(config.Value.Type)
				info.ConfigFieldsSet = append([]string(nil), config.FieldsSet...)
			}
			if !yield(info) {
				return nil
			}
		}
	}
	return nil
}

// IterateModuleInstances keeps every real instance in module/instance order,
// including an instance whose name no longer has a current enabled config.
func (s *Services) IterateModuleInstances(ctx context.Context) ([]ServiceInfo, error) {
	out := []ServiceInfo{}
	err := s.visit(ctx, func(info ServiceInfo) bool { out = append(out, info); return true })
	if err != nil {
		return nil, err
	}
	return out, nil
}

func serviceTypeFilter(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	key, err := canonicalName(raw)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(key, []byte(`null`)) {
		return nil, nil
	}
	return key, nil
}

// GetServices preserves dictionary insertion order and last matching value.
// Type filter nil/null means None, while "" remains an actual empty filter.
// An absent or empty names list means no name filter, as in the source set().
// These typed arguments admit nullable strings, not arbitrary Python objects.
func (s *Services) GetServices(ctx context.Context, typeFilter json.RawMessage, nameFilters []json.RawMessage) ([]ServiceInfo, error) {
	if err := contextOK(ctx); err != nil {
		return nil, err
	}
	filter, err := serviceTypeFilter(typeFilter)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, name := range nameFilters {
		key, err := canonicalName(name)
		if err != nil {
			return nil, err
		}
		names[string(key)] = true
	}
	out := []ServiceInfo{}
	positions := map[string]int{}
	err = s.visit(ctx, func(info ServiceInfo) bool {
		if info.Config == nil || filter != nil && !bytes.Equal(info.Type, filter) || len(names) != 0 && !names[string(info.Name)] {
			return true
		}
		key := string(info.Name)
		if position, ok := positions[key]; ok {
			out[position] = info
		} else {
			positions[key] = len(out)
			out = append(out, info)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetService returns the first matching actual instance, not GetServices[name]
// (which can refer to a later owner with the same name). Falsey name returns
// None without reading the store or traversing modules.
func (s *Services) GetService(ctx context.Context, name, typeFilter json.RawMessage) (*ServiceInfo, error) {
	if err := contextOK(ctx); err != nil {
		return nil, err
	}
	if len(name) == 0 {
		return nil, nil
	}
	key, err := canonicalName(name)
	if err != nil {
		return nil, err
	}
	if !nameTruthy(key) {
		return nil, nil
	}
	filter, err := serviceTypeFilter(typeFilter)
	if err != nil {
		return nil, err
	}
	var out *ServiceInfo
	err = s.visit(ctx, func(info ServiceInfo) bool {
		if !bytes.Equal(info.Name, key) || info.Config == nil || filter != nil && !bytes.Equal(info.Type, filter) {
			return true
		}
		out = &info
		return false
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
