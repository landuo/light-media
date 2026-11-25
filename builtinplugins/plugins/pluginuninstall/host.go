package pluginuninstall

import (
	"context"
	"encoding/json"
	"errors"

	plugins "light-media/builtinplugins/internal/sdk"
)

// Each continuation binds a fresh caller. The iterator yields before exhausting
// the standard per-call reverse-RPC budget; an expired caller is never reused.
type sdkOperations struct {
	ctx   context.Context
	host  plugins.HostCaller
	yield func() bool
	calls int
	fatal error
}

func (s *sdkOperations) caller() (context.Context, plugins.HostCaller, error) {
	if s.fatal != nil {
		return nil, nil, s.fatal
	}
	if s.calls == 40 {
		if !s.yield() {
			return nil, nil, errors.New("uninstall initializer closed")
		}
	}
	if s.ctx == nil || s.host == nil {
		return nil, nil, errors.New("uninstall phase not bound")
	}
	if err := s.ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.calls++
	return s.ctx, s.host, nil
}

func (s *sdkOperations) Installed(context.Context) ([]string, error) {
	ctx, host, err := s.caller()
	if err != nil {
		return nil, err
	}
	value, err := (plugins.SystemSettings{Host: host}).Get(ctx, "UserInstalledPlugins")
	if err != nil {
		return nil, err
	}
	var ids []string
	if string(value.Value) == "null" {
		return []string{}, nil
	}
	if err = json.Unmarshal(value.Value, &ids); err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (s *sdkOperations) control(id, action string) (plugins.PluginControlResult, error) {
	ctx, host, err := s.caller()
	if err != nil {
		return plugins.PluginControlResult{}, err
	}
	out, err := (plugins.PluginHost{Host: host}).Control(ctx, id, action)
	if err == nil && (!out.Accepted || !out.Completed) {
		err = errors.New("source manager operation did not complete")
	}
	return out, err
}
func (s *sdkOperations) RemoveJobs(_ context.Context, id string) error {
	_, err := s.control(id, "services.remove")
	return err
}
func (s *sdkOperations) RemovePlugin(_ context.Context, id string) error {
	_, err := s.control(id, "remove")
	return err
}
func (s *sdkOperations) PackageExists(_ context.Context, id string) (bool, error) {
	out, err := s.control(id, "code.exists")
	if err != nil {
		return false, err
	}
	var exists bool
	err = json.Unmarshal(out.Result, &exists)
	return exists, err
}
func (s *sdkOperations) RemovePackage(_ context.Context, id string) error {
	_, err := s.control(id, "code.remove")
	// The host suppresses ordinary rmtree errors itself. A transport/authority
	// error is uncertain, and must abort the following source operation.
	if err != nil {
		s.fatal = err
	}
	return err
}
func (s *sdkOperations) DeleteConfig(_ context.Context, id string, force bool) (bool, error) {
	ctx, host, err := s.caller()
	if err != nil {
		return false, err
	}
	return (plugins.PluginHost{Host: host}).DeleteConfig(ctx, id, force)
}
func (s *sdkOperations) DeleteData(_ context.Context, id string, force bool) (bool, error) {
	ctx, host, err := s.caller()
	if err != nil {
		return false, err
	}
	return (plugins.PluginHost{Host: host}).DeleteData(ctx, id, force)
}
func (s *sdkOperations) LogRemoved(_ context.Context, id string) error {
	ctx, host, err := s.caller()
	if err != nil {
		return err
	}
	return (plugins.Logger{Host: host}).Log(ctx, plugins.LogInfo, "插件 "+id+" 已卸载")
}
func (s *sdkOperations) SaveInstalled(_ context.Context, ids []string) error {
	ctx, host, err := s.caller()
	if err != nil {
		return err
	}
	_, err = (plugins.SystemSettings{Host: host}).Set(ctx, "UserInstalledPlugins", ids)
	return err
}
func (s *sdkOperations) SaveOwnConfig(_ context.Context, raw json.RawMessage) error {
	ctx, host, err := s.caller()
	if err != nil {
		return err
	}
	_, err = (plugins.Base{Host: host}).UpdateConfig(ctx, raw)
	return err
}
