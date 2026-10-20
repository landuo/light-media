package app

// BrushFlow's source configuration lives in the trusted host, not in a worker
// frame. Keep the complete original config and invocation authenticated while
// retaining the ordinary storage/protocol ceilings for every other adapter.
import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"strings"
	"time"
	"unicode/utf8"
)

const brushFlowSnapshotPrefix = "LM-BRUSH-SNAPSHOT-V1\x00"

func brushFlowSnapshotOwner(plugin, version string) bool {
	return plugin == bf.PluginID && version == bf.Version
}

func pluginSnapshotJSON(plugin, version string, value any, object bool) (json.RawMessage, error) {
	if !brushFlowSnapshotOwner(plugin, version) {
		return pluginJSON(value, object)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件数据不是有效JSON")
	}
	if object {
		if _, err = brushFlowConfig(raw); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func (p *PluginStore) pluginSnapshotOwner(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) (string, string, error) {
	var plugin, version string
	err := q.QueryRowContext(ctx, `SELECT plugin_id,version FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version)
	return plugin, version, err
}

func (p *PluginStore) pluginSnapshotJSON(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, value any, object bool) (json.RawMessage, error) {
	plugin, version, err := p.pluginSnapshotOwner(ctx, q, instance)
	if err != nil {
		return nil, err
	}
	return pluginSnapshotJSON(plugin, version, value, object)
}

func (p *PluginStore) sealPluginSnapshot(kind, instance, key, plugin, version string, raw []byte) ([]byte, error) {
	if (kind != "config" && kind != "run") || !brushFlowSnapshotOwner(plugin, version) || len(raw) <= pluginValueLimit {
		return p.seal(kind, instance, key, raw)
	}
	// A different AEAD purpose prevents stripping or adding the format marker
	// to turn an ordinary payload into a native large snapshot.
	sealed, err := p.seal("BrushFlowLanduo@5.2.15/snapshot/"+kind, instance, key, raw)
	if err != nil {
		return nil, err
	}
	return append([]byte(brushFlowSnapshotPrefix), sealed...), nil
}

func validateBrushFlowHTTPResponse(response plugins.HTTPResponse) error {
	// Retain the common status/header/content-type validation. This host-owned
	// provider returns its source JSON bodies without the generic 512KiB cap.
	metadata := response
	metadata.Body = json.RawMessage(`null`)
	if err := metadata.Validate(); err != nil {
		return err
	}
	typeName := response.Headers["Content-Type"]
	if typeName == "" || strings.HasPrefix(typeName, "application/json") {
		if !json.Valid(response.Body) {
			return errors.New("invalid native plugin JSON response")
		}
	} else if !utf8.Valid(response.Body) {
		return errors.New("invalid native plugin text response")
	}
	return nil
}

// A normal admitted lifecycle operation retains its existing live context for
// durable completion. The three-second detached budget belongs to cancellation
// cleanup, not successful source-sized configuration/receipt transactions.
func brushFlowCompletionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
}
