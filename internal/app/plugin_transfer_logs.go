package app

import (
	"context"
	"encoding/base64"
	transfer "light-media/internal/plugins/torrenttransfer"
	"net/url"
	"sort"
	"strings"
)

// Capture the original authorized downloader credentials before source IO so
// a later settings change cannot remove old-run redaction. Torrent bytes and
// announce/passkey values never appear in source log messages or SDK results.
func (a *App) transferSourceLogger(in PluginInstallation, run PluginRun, settings Settings) func(context.Context, transfer.Log) {
	variants := []string{}
	for _, d := range settings.Downloaders {
		for _, s := range []string{d.Password, d.APIKey} {
			if s != "" {
				variants = append(variants, s, url.QueryEscape(s), url.PathEscape(s), base64.StdEncoding.EncodeToString([]byte(s)))
			}
		}
	}
	sort.SliceStable(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	return func(_ context.Context, event transfer.Log) {
		message := event.Message
		for _, secret := range variants {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
		message = iyuuSourceSecretFields.ReplaceAllString(message, "${1}[REDACTED]")
		message = cleanWithSettings(message, settings)
		safeRun := run
		for _, secret := range variants {
			safeRun.RequestID = strings.ReplaceAll(safeRun.RequestID, secret, "[REDACTED]")
		}
		line := event.Line
		if in.PluginID == "TorrentTransfermusic" {
			line += 2
		}
		a.pluginExecutionLog(in, safeRun, event.Level, "torrent_transfer.source."+itoaSourceSite(int64(line)), message, "")
	}
}
