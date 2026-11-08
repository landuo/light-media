package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"light-media/internal/plugins"
	"light-media/internal/plugins/larkmessager"
)

const originalLarkEndpointCapability = "network.lark.endpoint"
const originalLarkEndpoint = "https://open.larksuite.com/open-apis"

type originalLarkWebhookKey struct{}

type originalLarkWebhookGrant struct {
	object   *originalPluginInstance
	verified bool
}

// The ordinary plugin webhook keeps MoviePilot's optional verification. A
// privileged native host action needs independent proof from the exact HTTP
// bytes and current configuration. The Go protocol decoder has no host side
// effects and handles the source's token, signatures and encrypted payloads.
func (a *App) originalLarkVerifiedWebhook(ctx context.Context, object *originalPluginInstance, input plugins.HTTPRequest) bool {
	raw, present, err := a.readOriginalBaseConfigForInit(ctx, object.installation.InstanceID)
	if err != nil || !present {
		return false
	}
	fields, err := larkmessager.DecodeSourceObject(raw)
	if err != nil {
		return false
	}
	config, err := larkmessager.ConfigFromTrusted(fields)
	if err != nil || config.EncryptKey == "" && config.VerificationToken == "" {
		return false
	}
	parsed, err := larkmessager.DecodeSourceWebhook(input.Body, input.Headers, input.LastQuery("token"), config.EncryptKey, config.VerificationToken)
	if err != nil || !parsed.Accepted {
		return false
	}
	if parsed.TokenVerified || parsed.SignatureVerified {
		return true
	}
	// MoviePilot exempts encrypted and card payloads from its ordinary
	// signature check. For a native site action, require a valid signature
	// or the verification token. AES-CBC decryption alone is not proof.
	signature := input.Headers["x-lark-signature"]
	timestamp := input.Headers["x-lark-request-timestamp"]
	nonce := input.Headers["x-lark-request-nonce"]
	return config.EncryptKey != "" && signature != "" && timestamp != "" && nonce != "" &&
		larkmessager.NewCrypto(config.EncryptKey).VerifySignature(signature, input.Body, timestamp, nonce)
}

func (a *App) originalLarkEndpointResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	object := scope.object
	call := scope.initialization
	if object == nil || object.app != a || object.installation.PluginID != "LarkMessager" || !object.external ||
		object.hostContract != pluginResourcesSDKContract || scope.phase != "init" || call == nil || originalLiveInitialization(ctx, object) != call {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "Lark API 地址只供实际初始化读取")
	}
	if err := originalResourceDecode(raw, &struct{}{}); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	endpoint := originalLarkEndpoint
	if a.pluginOriginalLarkTestOrigin != "" {
		if !originalLarkFixtureEndpoint(a.pluginOriginalLarkTestOrigin) {
			return nil, bad("NETWORK_CONFIGURATION_INVALID", "Lark 本地验收地址无效")
		}
		endpoint = a.pluginOriginalLarkTestOrigin
	}
	return json.Marshal(endpoint)
}

func originalLarkFixtureEndpoint(raw string) bool {
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "http" || address.User != nil || address.Opaque != "" ||
		address.Path != "/open-apis" || address.RawPath != "" || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(address.Port())
	return err == nil && port > 0 && port <= 65535 && raw == "http://127.0.0.1:"+strconv.Itoa(port)+"/open-apis"
}
