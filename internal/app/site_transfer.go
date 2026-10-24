package app

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/net/publicsuffix"
	"light-media/internal/sites"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const siteExportFormat = "light-media-sites"

type siteExportDocument struct {
	Format  string       `json:"format"`
	Version int          `json:"version"`
	Sites   []SiteConfig `json:"sites"`
}

type siteImportResult struct {
	Index    int      `json:"index"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	SiteID   string   `json:"site_id,omitempty"`
	Code     string   `json:"code,omitempty"`
	Message  string   `json:"message"`
	Warnings []string `json:"warnings"`
}

type siteImportReport struct {
	Total    int                `json:"total"`
	Imported int                `json:"imported"`
	Created  int                `json:"created"`
	Updated  int                `json:"updated"`
	Skipped  int                `json:"skipped"`
	Failed   int                `json:"failed"`
	Results  []siteImportResult `json:"results"`
}

// Imported origins have a stricter boundary than the local fixture seam in
// normalizeManagedSite: a file cannot introduce localhost or arbitrary ports.
// The adapter is derived from an exact host match, never from its display name.
func siteImportOrigin(value string) (kind, canonical, domain string, err error) {
	u, e := url.Parse(strings.TrimSpace(value))
	if e != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return "", "", "", bad("INVALID_SITE_URL", "站点地址必须是 HTTPS 根地址，不支持自定义端口、路径或参数")
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "m-team.cc", "www.m-team.cc", "kp.m-team.cc", "api.m-team.cc", "test2.m-team.cc":
		kind, domain = "mteam", "m-team.cc"
		if host != "test2.m-team.cc" {
			host = "api.m-team.cc"
		}
	case "m-team.io", "www.m-team.io", "kp.m-team.io", "api.m-team.io":
		kind, domain, host = "mteam", "m-team.io", "api.m-team.io"
	case "rousi.pro", "www.rousi.pro":
		kind, domain, host = "rousi", "rousi.pro", "rousi.pro"
	case "hhanclub.net", "www.hhanclub.net":
		kind, domain = "hhanclub", "hhanclub.net"
	case "ubits.club", "www.ubits.club":
		kind, domain = "ubits", "ubits.club"
	case "hdhome.org", "www.hdhome.org":
		kind, domain = "jiayuan", "hdhome.org"
	case "1ptba.com", "www.1ptba.com":
		kind, domain = "1ptba", "1ptba.com"
	default:
		canonical, err = sites.NormalizeNexusPHPOrigin(value)
		if err != nil {
			return "", "", "", bad("INVALID_SITE_URL", "站点地址须为公网域名的 HTTPS 根地址")
		}
		domain, err = publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			domain = host
		}
		return "nexusphp", strings.TrimRight(canonical, "/"), domain, nil
	}
	return kind, "https://" + host, domain, nil
}

func siteImportName(kind string) string {
	for _, template := range nativeSiteTemplates {
		if template.Kind == kind {
			return template.Name
		}
	}
	return ""
}

func invalidSiteImportText(value string, limit int) bool {
	return len(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0
}

func decodeSiteImportEntry(raw json.RawMessage, native bool) (SiteConfig, []string, error) {
	in := SiteConfig{Enabled: true, DailyBudget: 600}
	warnings := []string{}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return in, warnings, bad("INVALID_SITE_ENTRY", "站点字段格式无效，请核对地址、数值及开关字段")
	}
	// MoviePilot uses numeric database IDs. Imported IDs never identify local
	// records; the normalized origin below is the sole migration identity.
	delete(fields, "id")
	config, _ := json.Marshal(fields)
	if json.Unmarshal(config, &in) != nil {
		return in, warnings, bad("INVALID_SITE_ENTRY", "站点字段格式无效，请核对地址、数值及开关字段")
	}
	if own, budget := siteImportExtension(raw); own {
		in.DailyBudget = budget
	}
	if in.Proxy || in.Render {
		warnings = append(warnings, "代理与浏览器仿真选项已保留；当前连接不启用这两项能力")
	}
	if in.RSS != "" || in.Downloader != "" || in.Filter != "" || in.Token != "" {
		warnings = append(warnings, "RSS、下载器绑定、过滤规则及 Token 已保留供导出；当前请求使用 Cookie 或站点专用 API Key")
	}
	kind, canonical, actualDomain, err := siteImportOrigin(in.BaseURL)
	if err != nil {
		return in, warnings, err
	}
	if native && in.Kind != "" && in.Kind != kind {
		return in, warnings, bad("SITE_ORIGIN_MISMATCH", "站点类型与地址不匹配，未导入")
	}
	u, _ := url.Parse(canonical)
	domain := strings.ToLower(strings.TrimSpace(in.Domain))
	if domain != "" && domain != actualDomain && domain != u.Hostname() && !(kind == "nexusphp" && strings.HasSuffix(u.Hostname(), "."+domain)) {
		return in, warnings, bad("SITE_ORIGIN_MISMATCH", "MoviePilot 站点域名与地址不匹配，未导入")
	}
	in.Kind, in.BaseURL, in.ID = kind, canonical, ""
	if in.Domain == "" {
		in.Domain = actualDomain
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 || invalidSiteImportText(in.Name, 256) {
		return in, warnings, bad("INVALID_SITE_NAME", "站点名称不能为空且最长 64 字，不能包含控制字符")
	}
	if invalidSiteImportText(in.Cookie, 64<<10) || invalidSiteImportText(in.APIKey, 8192) || invalidSiteImportText(in.Token, 8192) || invalidSiteImportText(in.UserAgent, 8192) {
		return in, warnings, bad("INVALID_SITE_CREDENTIAL", "站点登录信息格式无效或过长")
	}
	if in.DailyBudget < 0 || in.DailyBudget > 10000 {
		return in, warnings, bad("INVALID_SITE_BUDGET", "每日请求预算须在 0 至 10000 之间")
	}
	return in, warnings, nil
}

func siteImportExtension(raw json.RawMessage) (bool, int) {
	var entry struct {
		Extension *struct {
			Version     int `json:"version"`
			DailyBudget int `json:"daily_budget"`
		} `json:"_light_media"`
	}
	if json.Unmarshal(raw, &entry) == nil && entry.Extension != nil && entry.Extension.Version == 1 {
		return true, entry.Extension.DailyBudget
	}
	return false, 0
}

func (a *App) importManagedSites(ctx context.Context, raw json.RawMessage) (siteImportReport, error) {
	report := siteImportReport{Results: []siteImportResult{}}
	var entries []json.RawMessage
	input := strings.TrimSpace(string(raw))
	native := strings.HasPrefix(input, "{")
	if native {
		var document struct {
			Format  string            `json:"format"`
			Version int               `json:"version"`
			Sites   []json.RawMessage `json:"sites"`
		}
		if json.Unmarshal(raw, &document) != nil || document.Format != siteExportFormat || document.Version != 1 {
			return report, bad("INVALID_SITE_IMPORT", "请选择本应用或 MoviePilot 导出的站点 JSON 文件")
		}
		entries = document.Sites
	} else if !strings.HasPrefix(input, "[") || json.Unmarshal(raw, &entries) != nil {
		return report, bad("INVALID_SITE_IMPORT", "请选择本应用或 MoviePilot 导出的站点 JSON 文件")
	}
	if len(entries) == 0 {
		return report, bad("SITE_IMPORT_EMPTY", "文件中没有可导入的站点")
	}
	if len(entries) > 128 {
		return report, bad("SITE_IMPORT_LIMIT", "导入文件须包含 1 至 128 个站点条目")
	}
	report.Total = len(entries)
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	config := a.Settings()
	// Settings returns a struct snapshot; detach the site slice before staging.
	config.Sites = append([]SiteConfig(nil), config.Sites...)
	seen := map[string]bool{}
	touched := map[string]bool{}
	identityRefs := []importedSiteIdentity{}
	documentFingerprint := a.Store.siteIdentityMAC("import-document", []byte(raw))
	for index, entry := range entries {
		own, _ := siteImportExtension(entry)
		result := siteImportResult{Index: index + 1, Name: fmt.Sprintf("第 %d 项", index+1), Status: "failed", Warnings: []string{}}
		in, warnings, err := decodeSiteImportEntry(entry, native)
		originalNumericID, identityErr := importedNumericSiteID(entry)
		if err == nil && identityErr != nil {
			err = identityErr
		}
		result.Warnings = warnings
		if err == nil {
			result.Name = siteImportName(in.Kind)
			if result.Name == "" || in.Kind == "nexusphp" {
				origin, _ := url.Parse(in.BaseURL)
				result.Name = origin.Hostname()
			}
			identity := in.Kind + "|" + in.BaseURL
			if seen[identity] {
				err = bad("DUPLICATE_SITE_IMPORT", "文件中同一站点地址重复，后续条目未导入")
			} else {
				seen[identity] = true
			}
		}
		found := -1
		if err == nil {
			for i, old := range config.Sites {
				_, origin, _, originErr := siteImportOrigin(old.BaseURL)
				if originErr == nil && origin == in.BaseURL {
					if found >= 0 {
						err = bad("AMBIGUOUS_SITE", "已有多个相同站点地址，请先在站点管理中核对")
						break
					}
					found = i
				}
			}
		}
		if err == nil {
			if found >= 0 {
				old := config.Sites[found]
				in.ID = old.ID
				if touched[old.ID] {
					err = bad("DUPLICATE_SITE_IMPORT", "文件中多个条目对应同一已有站点，后续条目未导入")
				}
				if !native && !own {
					in.DailyBudget = old.DailyBudget
				}
				// A changed origin must supply its own credential; do not move the
				// saved cookie/API key based only on a site name or adapter kind.
				_, origin, _, originErr := siteImportOrigin(old.BaseURL)
				if originErr == nil && origin == in.BaseURL {
					preserveSiteSecrets(&in, old)
				}
			} else {
				in.ID = id("site")
				if len(config.Sites) >= 128 {
					err = bad("SITE_LIMIT", "已达到 128 个站点的配置上限")
				}
			}
			if err == nil && normalizeManagedSite(&in) != nil {
				err = bad("INVALID_SITE_ENTRY", "站点配置未通过校验")
			}
		}
		if err != nil {
			apiErr, ok := err.(*APIError)
			if !ok {
				return report, bad("SITE_IMPORT_FAILED", "站点导入失败，配置未保存")
			}
			result.Code, result.Message = apiErr.Code, apiErr.Message
			if apiErr.Code == "SITE_NOT_ADAPTED" {
				result.Status = "skipped"
				report.Skipped++
			} else {
				report.Failed++
			}
		} else {
			if ((in.Kind == "mteam" || in.Kind == "rousi") && in.APIKey == "") || (in.Kind != "mteam" && in.Kind != "rousi" && in.Cookie == "") {
				result.Warnings = append(result.Warnings, "登录凭据尚未配置；已保存站点，补充凭据后再测试连接")
			}
			touched[in.ID] = true
			result.SiteID = in.ID
			if found >= 0 {
				config.Sites[found] = in
				result.Status, result.Message = "updated", "已更新站点配置，保留原站点 ID；尚未测试连接"
				report.Updated++
			} else {
				config.Sites = append(config.Sites, in)
				result.Status, result.Message = "created", "已导入站点配置；尚未测试连接"
				report.Created++
			}
			report.Imported++
			if originalNumericID > 0 {
				identityRefs = append(identityRefs, importedSiteIdentity{DocumentFingerprint: documentFingerprint, Ordinal: index + 1, OriginalSourceID: originalNumericID, SiteID: in.ID, CanonicalOrigin: in.BaseURL})
			}
		}
		report.Results = append(report.Results, result)
	}
	if report.Imported > 0 {
		if err := a.saveSettings(context.WithValue(ctx, importedSiteIdentitiesKey{}, identityRefs), config); err != nil {
			return siteImportReport{}, bad("SITE_IMPORT_FAILED", "站点配置保存失败，本次导入未生效")
		}
	}
	return report, nil
}

func (a *App) exportManagedSites(w http.ResponseWriter, r *http.Request) {
	configured := a.Settings().Sites
	document := make([]map[string]any, 0, len(configured))
	for _, s := range configured {
		document = append(document, map[string]any{
			"name": s.Name, "domain": s.Domain, "url": s.BaseURL,
			"cookie": s.Cookie, "apikey": s.APIKey, "token": s.Token, "ua": s.UserAgent,
			"rss": s.RSS, "downloader": s.Downloader, "filter": s.Filter,
			"proxy": s.Proxy, "render": s.Render, "public": s.Public, "note": s.Note,
			"timeout": s.Timeout, "pri": s.Priority, "is_active": s.Enabled,
			"limit_interval": s.LimitInterval, "limit_count": s.LimitCount, "limit_seconds": s.LimitSeconds,
			"_light_media": map[string]any{"version": 1, "daily_budget": s.DailyBudget},
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Disposition", `attachment; filename="light-media-sites-`+time.Now().In(shanghai()).Format("20060102")+`.json"`)
	_ = json.NewEncoder(w).Encode(document)
}
