package app

import (
	"encoding/json"
	"light-media/internal/plugins/artifacts"
)

func (p pluginCard) MarshalJSON() ([]byte, error) {
	if p.SourceDisplay != nil {
		if err := p.SourceDisplay.WireError(); err != nil {
			return nil, err
		}
	}
	type plain pluginCard
	base := plain(p)
	if p.SourceDisplay != nil {
		base.SystemVersion = nil
	}
	raw, err := json.Marshal(base)
	if err != nil || p.SourceDisplay == nil {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for key, value := range p.SourceDisplay.CardValues() {
		fields[key] = value
	}
	fields["repository_version"] = p.SourceDisplay.Values()["version"]
	return json.Marshal(fields)
}
func originalPythonHistoryJSON(display *artifacts.PythonDisplay, raw []byte, release bool) ([]byte, error) {
	if display == nil {
		return raw, nil
	}
	field := "history"
	if release {
		field = "version"
	}
	if err := display.WireError(field); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	values := display.Values()
	fields["history"], fields["latest_version"] = values["history"], values["version"]
	return json.Marshal(fields)
}
func originalMarketDeclarationKey(source artifacts.Discovery) string {
	if source.SourceDisplay != nil {
		return source.PluginID + source.SourceDisplay.VersionKey
	}
	return source.PluginID + source.CurrentVersion
}
func originalMarketStringVersion(source artifacts.Discovery) (string, bool) {
	if source.SourceDisplay != nil {
		return source.SourceDisplay.StringVersion()
	}
	return source.CurrentVersion, true
}

// Source compare_version catches non-string .strip() failures. Stringifying a
// number for the concat key must never also turn it into a comparable version.
func originalMarketVersionGreater(left, right artifacts.Discovery) bool {
	a, okA := originalMarketStringVersion(left)
	b, okB := originalMarketStringVersion(right)
	return okA && okB && compareOriginalPluginVersion(a, b) > 0
}
func originalMarketVersionEqual(left, right artifacts.Discovery) bool {
	// The only call site uses equality for an online-versus-local tie. Local Go
	// declarations always have validated string versions, not Python JSON values.
	a, okA := originalMarketStringVersion(left)
	b, okB := originalMarketStringVersion(right)
	return okA && okB && a == b
}

func originalMarketCardsWireError(items []pluginCard) error {
	for _, card := range items {
		if card.SourceDisplay != nil {
			if err := card.SourceDisplay.WireError(); err != nil {
				return originalMarketDisplayFailure()
			}
		}
	}
	return nil
}
func originalMarketDisplayFailure() error {
	return &APIError{Code: "PLUGIN_MARKET_SERIALIZATION_FAILED", Message: "插件市场显示数据无法序列化", Status: 500}
}
