package plugins

import (
	"context"
	"errors"
)

const NetworkProxyReadCapability = "network.proxy.read"

// NetworkProxySettingsV1 is a private snapshot for trusted plugin Go code.
// Proxy URLs may contain credentials. Keep it in memory, never in public
// configuration, plugin data, logs or reports. Proxies=nil matches source None.
// Environment contains only proxy variables and REQUEST_METHOD presence/value,
// including case and host-specific keys required by requests' merge policy.
type NetworkProxySettingsV1 struct {
	SchemaVersion int               `json:"schema_version"`
	Proxies       map[string]string `json:"proxies"`
	Environment   []string          `json:"environment"`
}

func (NetworkProxySettingsV1) String() string   { return "private proxy snapshot [redacted]" }
func (NetworkProxySettingsV1) GoString() string { return "private proxy snapshot [redacted]" }

func (n Network) ProxySettings(ctx context.Context) (NetworkProxySettingsV1, error) {
	var out NetworkProxySettingsV1
	err := hostBusinessCall(ctx, n.Host, NetworkProxyReadCapability, struct{}{}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Environment == nil) {
		err = errors.New("invalid private proxy snapshot")
	}
	return out, err
}
