package app

// Fixed original class metadata, separate from installed sealed manifests.
// Sources and exact-hash limits are recorded in the original-order evidence.
var originalCompiledOrders = map[string]int{
	"AutoBackup":           17,
	"AutoSignIn":           0,
	"BrushFlowLanduo":      21,
	"CategoryEditor":       5,
	"CleanInvalidSeed":     1,
	"CloudflareSpeedTest":  12,
	"CustomHosts":          10,
	"IYUUAutoSeed":         17,
	"MPServerStatus":       99,
	"MediaServerRefresh":   14,
	"MergeSiteSwitch":      66,
	"PluginUnInstall":      98,
	"RemoteIdentifiers":    10,
	"RemoveLink":           0,
	"ScrapeFileClean":      0,
	"SeedRescue":           18,
	"SiteStatistic":        1,
	"SubscribeGroup":       26,
	"SystemProcess":        66,
	"TorrentTransfer":      18,
	"TorrentTransfermusic": 18,
}

func originalCompiledPluginOrder(plugin string) int { return originalCompiledOrders[plugin] }
