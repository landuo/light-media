package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// App assembly must publish a real Services built from its actual loaded
// Manager/private raw reader. Nil means unassembled, not an empty registry.
// This bridge never constructs, reloads or closes a media vendor.
type originalMediaServerHost struct{ services *mediaserverruntime.Services }

func newOriginalMediaServerHost(services *mediaserverruntime.Services) (*originalMediaServerHost, error) {
	if services == nil {
		return nil, errors.New("media instance host requires actual services")
	}
	return &originalMediaServerHost{services: services}, nil
}

// Embedded in the exact persistent originalPluginInstance, not its callback,
// config revision or name. Retirement seals even a never-used zero value.
type originalMediaServerReferenceOwner struct {
	mu         sync.Mutex
	closed     bool
	references *mediaserverruntime.ObjectReferences
	libraries  *originalMediaServerLibraryReferences
}

func (o *originalMediaServerReferenceOwner) table() (*mediaserverruntime.ObjectReferences, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, mediaserverruntime.ErrReferenceClosed
	}
	if o.references == nil {
		o.references = mediaserverruntime.NewObjectReferences()
	}
	return o.references, nil
}
func (o *originalMediaServerReferenceOwner) close() {
	o.mu.Lock()
	o.closed = true
	table := o.references
	libraries := o.libraries
	o.mu.Unlock()
	if table != nil {
		table.Close()
	}
	if libraries != nil {
		libraries.close()
	}
}

// A concrete client's Current adapter can consume this host-only operation
// context. Worker JSON cannot create it. Constructor/reload admission requires
// its own real host lifecycle adapter and is not fabricated in this bridge.
type originalMediaServerOperationKey struct{}
type originalMediaServerOperation struct {
	current mediaserverruntime.ReferenceCurrent
}

func originalMediaServerOperationCurrent(ctx context.Context) error {
	if ctx == nil {
		return bad("PLUGIN_CALL_CLOSED", "媒体实例操作缺少调用上下文")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	call, ok := ctx.Value(originalMediaServerOperationKey{}).(*originalMediaServerOperation)
	if !ok || call == nil || call.current == nil {
		return bad("PLUGIN_CALL_CLOSED", "媒体实例操作未绑定当前调用")
	}
	return call.current(ctx)
}
func originalSharedMediaServerInstanceCapability(method string) bool {
	return method == plugins.MediaServerGetServicesCapability || method == plugins.MediaServerGetServiceCapability || method == plugins.MediaServerIsInactiveCapability || originalMediaServerRefreshCapability(method)
}

func (a *App) originalSharedMediaServerInstanceResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if ctx == nil || scope.validate == nil || !originalSharedMediaServerInstanceCapability(method) || scope.object == nil || scope.object.app != a {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体实例操作未绑定当前对象")
	}
	current := mediaserverruntime.ReferenceCurrent(func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return scope.validate(ctx)
	})
	if err := current(ctx); err != nil {
		return nil, err
	}
	references, err := scope.object.mediaReferences.table()
	if err != nil {
		return nil, err
	}
	// The resource dispatcher already verified full PluginID/InstanceID/Version
	// and this exact method's manifest grant. No lock is held across the guard,
	// query, entropy acquisition or vendor method.
	ctx = context.WithValue(ctx, originalMediaServerOperationKey{}, &originalMediaServerOperation{current: current})
	if originalMediaServerRefreshCapability(method) {
		return a.originalMediaServerRefreshResource(ctx, current, &scope.object.mediaReferences, references, method, raw)
	}
	var result any
	if method == plugins.MediaServerIsInactiveCapability {
		var input plugins.MediaServerInstanceRequest
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		vendor, err := references.Resolve(ctx, current, input.Reference)
		if err != nil {
			return nil, err
		}
		if err := current(ctx); err != nil {
			return nil, err
		}
		inactive, callErr := vendor.IsInactive(ctx)
		return originalMediaServerServiceResult(ctx, current, inactive, callErr)
	}
	a.pluginMu.Lock()
	host := a.originalMediaServerInstances
	a.pluginMu.Unlock()
	if err := current(ctx); err != nil {
		return nil, err
	}
	if host == nil || host.services == nil {
		return nil, bad("MEDIA_SERVER_INSTANCES_NOT_ESTABLISHED", "媒体服务器实例尚未装配")
	}
	if method == plugins.MediaServerGetServicesCapability {
		var input plugins.MediaServerServicesRequest
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		infos, err := host.services.GetServices(ctx, input.Type, input.Names)
		if err != nil {
			return originalMediaServerServiceResult(ctx, current, nil, err)
		}
		out := make([]plugins.MediaServerServiceInfo, 0, len(infos))
		for _, info := range infos {
			value, err := originalMediaServerServiceProjection(ctx, current, references, info)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		result = out
	} else {
		var input plugins.MediaServerServiceRequest
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		info, err := host.services.GetService(ctx, input.Name, input.Type)
		if err != nil {
			return originalMediaServerServiceResult(ctx, current, nil, err)
		}
		if info != nil {
			value, err := originalMediaServerServiceProjection(ctx, current, references, *info)
			if err != nil {
				return nil, err
			}
			result = value
		}
	}
	return originalMediaServerServiceResult(ctx, current, result, nil)
}
func originalMediaServerServiceProjection(ctx context.Context, current mediaserverruntime.ReferenceCurrent, references *mediaserverruntime.ObjectReferences, info mediaserverruntime.ServiceInfo) (plugins.MediaServerServiceInfo, error) {
	token, err := references.Retain(ctx, current, info.Instance)
	if err != nil {
		return plugins.MediaServerServiceInfo{}, err
	}
	// Services owns these fresh per-query model values. Marshal detaches them
	// across the process boundary; the table retains only the concrete Vendor.
	return plugins.MediaServerServiceInfo{Name: info.Name, Type: info.Type, Config: info.Config, ConfigFieldsSet: info.ConfigFieldsSet, Instance: plugins.MediaServerInstance{Reference: token}}, nil
}
func originalMediaServerServiceResult(ctx context.Context, current mediaserverruntime.ReferenceCurrent, result any, callErr error) (json.RawMessage, error) {
	if err := current(ctx); err != nil {
		return nil, err
	}
	response := plugins.MediaServerServicesResponse{}
	if callErr != nil {
		source, ok := callErr.(*mediaserverruntime.SourceError)
		if !ok || source == nil || source.Class == "" || errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			return nil, callErr
		}
		response.Result = json.RawMessage("null")
		response.SourceErrorClass = source.Class
	} else {
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		response.Result = encoded
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "媒体实例结果超出既有SDK传输范围")
	}
	if err := current(ctx); err != nil {
		return nil, err
	}
	return encoded, nil
}
