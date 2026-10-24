package builtin

import "encoding/json"

// CloneExecutionDefaults names only reviewed immediate initialization triggers
// in the current compiled catalog. It is not inferred from field names and is
// never supplied by a worker, request or stored manifest. Missing keys stay
// missing. Normal policies and later-action options are not rewritten.
func CloneExecutionDefaults(pluginID, version string) map[string]json.RawMessage {
	manifest, _, ok := Lookup(pluginID)
	if !ok || manifest.Version != version {
		return nil
	}
	keys := []string{}
	switch pluginID {
	case "AutoSignIn", "TorrentTransfer", "TorrentTransfermusic", "SeedRescue", "CloudflareSpeedTest", "AutoBackup", "RemoteIdentifiers", "SiteStatistic", "CleanInvalidSeed":
		keys = []string{"onlyonce"}
	case "IYUUAutoSeed":
		keys = []string{"onlyonce", "clearcache"}
	case "PluginUnInstall":
		return map[string]json.RawMessage{"plugin_ids": json.RawMessage(`[]`)}
	}
	result := make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		result[key] = json.RawMessage(`false`)
	}
	return result
}
