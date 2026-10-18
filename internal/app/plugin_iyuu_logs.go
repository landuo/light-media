package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

var iyuuSourceSecretFields = regexp.MustCompile(`(?i)(["']?(?:passkey|rsskey|token|api[_-]?key|authorization|cookie|password|passwd|secret|authkey)["']?\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s,;&}]+)`)

// The run and known credentials are captured before source execution. Later
// settings edits cannot remove redaction for an older run still writing logs.
func (a *App) iyuuSourceLogContext(ctx context.Context, in PluginInstallation, run PluginRun, config json.RawMessage, settings Settings) context.Context {
	secrets := []string{}
	var collect func(any)
	collect = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, v := range item {
				normal := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
				switch normal {
				case "passkey", "rsskey", "token", "apikey", "accesstoken", "authorization", "cookie", "password", "passwd", "secret", "authkey":
					if secret, ok := v.(string); ok {
						secrets = append(secrets, secret)
					}
				}
				collect(v)
			}
		case []any:
			for _, v := range item {
				collect(v)
			}
		}
	}
	if raw, e := json.Marshal(settings); e == nil {
		var snapshot any
		if json.Unmarshal(raw, &snapshot) == nil {
			collect(snapshot)
		}
	}
	var fields map[string]any
	if json.Unmarshal(config, &fields) == nil {
		collect(fields)
		if token, ok := fields["token"].(string); ok {
			secrets = append(secrets, token)
		}
	}
	for _, s := range settings.Sites {
		secrets = append(secrets, s.Cookie, s.APIKey, s.Token, s.RSS, string(s.Note))
	}
	for _, d := range settings.Downloaders {
		secrets = append(secrets, d.Password)
	}
	// Custom-site credentials can be returned within source formatting/error
	// messages. This is an existing authorized peer projection, not user grants.
	if custom, e := a.iyuuCustomSites(ctx); e == nil {
		for _, site := range custom {
			collect(site)
			for _, key := range []string{"cookie", "apikey", "token", "passkey", "rss", "password"} {
				if v, ok := site[key].(string); ok {
					secrets = append(secrets, v)
				}
			}
		}
	}
	variants := []string{}
	for _, secret := range secrets {
		if secret != "" {
			variants = append(variants, secret, url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)))
		}
	}
	sort.SliceStable(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	return iyuu.WithSourceLogSink(ctx, func(_ context.Context, event iyuu.SourceLog) {
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
		a.pluginExecutionLog(in, safeRun, event.Level, "iyuu.source."+itoaSourceSite(int64(event.Line)), message, "")
	})
}
