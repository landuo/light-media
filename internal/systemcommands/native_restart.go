package systemcommands

import "context"

// NativeRestartPorts intentionally has no version/HTTP/frontend ports. It
// connects the selected restart algorithm to the existing managed native code
// backup, without pretending its artifact layout is a Python plugin tree.
type NativeRestartPorts struct {
	Guard   func(context.Context) error
	Notify  func(context.Context, Notice) error
	Log     func(context.Context, string, string) error
	Cache   Cache
	Codec   Codec
	Backup  func(context.Context) error
	Restart func(context.Context) (any, error)
}

type NativeRestart struct {
	engine *Engine
	backup func(context.Context) error
}

func NewNativeRestart(markerKey string, ports NativeRestartPorts) (*NativeRestart, error) {
	if markerKey == "" || ports.Notify == nil || ports.Log == nil || ports.Cache == nil || ports.Codec == nil || ports.Backup == nil || ports.Restart == nil {
		return nil, boundary("incomplete native restart dependencies")
	}
	engine := &Engine{configuration: Configuration{MarkerKey: markerKey}, ports: Ports{
		Guard: ports.Guard, Notify: ports.Notify, Log: ports.Log,
		Cache: ports.Cache, Codec: ports.Codec, Restart: ports.Restart,
	}}
	return &NativeRestart{engine: engine, backup: ports.Backup}, nil
}

// Restart keeps SystemChain.restart's original notification/save/backup/helper
// order and return-value semantics. The accepted Engine helpers own the exact
// truthiness, source-caught save failures and before/after authority checks.
// Only the backup body is the explicit native managed-artifact adaptation.
func (r *NativeRestart) Restart(ctx context.Context, channel *Channel, userid any, source *string) error {
	e := r.engine
	if err := e.check(ctx); err != nil {
		return err
	}
	if channel != nil {
		truth, err := userTruthy(userid)
		if err != nil {
			return err
		}
		if truth {
			if err = e.notify(ctx, Notice{Channel: channel, UserID: userid, Source: source, IncludeSource: true, Title: "系统正在重启，请耐心等候！"}); err != nil {
				return err
			}
			if err = e.save(ctx, map[string]any{"channel": channel.Value, "userid": userid}); err != nil {
				return err
			}
		}
	}
	err := action(e, ctx, func() error { return r.backup(ctx) })
	if caught, ok := ordinary(err); ok {
		err = e.log(ctx, "error", "插件备份失败: "+caught.Message)
	}
	if err != nil {
		return err
	}
	_, err = effect(e, ctx, func() (any, error) { return e.ports.Restart(ctx) })
	return err
}
