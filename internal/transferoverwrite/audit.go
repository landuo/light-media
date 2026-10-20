package transferoverwrite

import "context"

// These exact installed versions were audited from all 23 roots / 76 actual
// Python sources. All 15 registration calls have statically named event enums;
// none registers TransferOverwriteCheck or TransferIntercept. No related source
// reference or parse failure was found. Other installations are not assumed empty.
// Audit SHA256: b4a99396015517b545dcb2c3c59c88691498e31797104e400e73e27a8c9e5fc1.
var auditedVersions = map[string]string{
	"AutoBackup": "2.1.4", "AutoSignIn": "2.9.14", "BrushFlowLanduo": "5.2.15", "CategoryEditor": "1.3", "CleanInvalidSeed": "2.1", "CloudflareSpeedTest": "1.5.1", "CustomHosts": "1.2.1", "IYUUAutoSeed": "2.19", "LarkMessager": "0.9.1", "MPServerStatus": "1.4", "MediaServerRefresh": "1.3.3", "MergeMessageNotify": "1.20.1", "MergeSiteSwitch": "1.2", "PluginUnInstall": "2.2", "RemoteIdentifiers": "2.4.1", "RemoveLink": "2.17", "ScrapeFileClean": "1.0.7", "SeedRescue": "1.6.3", "SiteStatistic": "1.9.7", "SubscribeGroup": "2.8.7", "SystemProcess": "1.1", "TorrentTransfer": "1.10.3", "TorrentTransfermusic": "1.10.3",
}

type Installation struct{ ID, Version string }

// AuditedDispatcher returns the original no-handler result (None). Callers must
// supply the actual installed list, not a list filtered by supported versions.
func AuditedDispatcher(installed []Installation) (Dispatch, error) {
	for _, i := range installed {
		if auditedVersions[i.ID] != i.Version || i.Version == "" {
			return nil, ErrDependency
		}
	}
	return func(ctx context.Context, _ Event) (*Event, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}, nil
}
