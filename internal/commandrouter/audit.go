package commandrouter

import "errors"

var ErrCommandDependency = errors.New("source_command_registry_dependency")

type Installation struct{ ID, Version string }

// Actual installed23 roots/76 files, all15 static event registrations and22
// get_command definitions: no preset override or CommandExcute consumer.
// Only Lark supplies get_module, with13 statically named entries and no
// list_torrents override.
// Unknown/new native installations cannot be assumed to leave this registry intact.
// Audit SHA256: f353bc614fe9da33549afc86902e3f06e7552347f28a19b22dce5c6f684141bd
var versions = map[string]string{
	"IYUUAutoSeed":         "2.19",
	"CustomHosts":          "1.2.1",
	"CloudflareSpeedTest":  "1.5.1",
	"RemoveLink":           "2.17",
	"MPServerStatus":       "1.4",
	"TorrentTransfer":      "1.10.3",
	"AutoSignIn":           "2.9.14",
	"MediaServerRefresh":   "1.3.3",
	"RemoteIdentifiers":    "2.4.1",
	"CleanInvalidSeed":     "2.1",
	"SiteStatistic":        "1.9.7",
	"CategoryEditor":       "1.3",
	"SystemProcess":        "1.1",
	"MergeSiteSwitch":      "1.2",
	"PluginUnInstall":      "2.2",
	"SeedRescue":           "1.6.3",
	"SubscribeGroup":       "2.8.7",
	"BrushFlowLanduo":      "5.2.15",
	"TorrentTransfermusic": "1.10.3",
	"MergeMessageNotify":   "1.20.1",
	"ScrapeFileClean":      "1.0.7",
	"LarkMessager":         "0.9.1",
	"AutoBackup":           "2.1.4",
}

func AdmitPreset(installed []Installation) error {
	for _, i := range installed {
		if i.Version == "" || versions[i.ID] != i.Version {
			return ErrCommandDependency
		}
	}
	return nil
}
