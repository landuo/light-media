package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"light-media/internal/sites"
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

// MoviePilot's SiteCardListView exports an unwrapped array using these names.
// Fields the application cannot honor are reported, never interpreted as auth.
type moviePilotSiteImport struct {
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	URL        string `json:"url"`
	Cookie     string `json:"cookie"`
	APIKey     string `json:"apikey"`
	Token      string `json:"token"`
	UA         string `json:"ua"`
	RSS        string `json:"rss"`
	Downloader string `json:"downloader"`
	Filter     string `json:"filter"`
	Proxy      int    `json:"proxy"`
	Render     int    `json:"render"`
	Public     int    `json:"public"`
	Timeout    int    `json:"timeout"`
	Interval   int    `json:"limit_interval"`
	Count      int    `json:"limit_count"`
	Seconds    int    `json:"limit_seconds"`
	Priority   int    `json:"pri"`
	Active     *bool  `json:"is_active"`
}

// Imported origins have a stricter boundary than the local fixture seam in
// normalizeManagedSite: a file cannot introduce localhost or arbitrary ports.
// The adapter is derived from an exact host match, never from its display name.
func siteImportOrigin(value string) (kind, canonical, domain string, err error) {
	u, e := url.Parse(strings.TrimSpace(value))
	if e != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return "", "", "", bad("INVALID_SITE_URL", "站点地址必须是已适配站点的 HTTPS 根地址，不支持自定义端口、路径或参数")
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
	case "hhanclub.net", "www.hhanclub.net":
		kind, domain = "hhanclub", "hhanclub.net"
	case "ubits.club", "www.ubits.club":
		kind, domain = "ubits", "ubits.club"
	case "hdhome.org", "www.hdhome.org":
		kind, domain = "jiayuan", "hdhome.org"
	case "1ptba.com", "www.1ptba.com":
		kind, domain = "1ptba", "1ptba.com"
	default:
		return "", "", "", bad("SITE_NOT_ADAPTED", "该域名尚未适配，已跳过；目前仅支持馒头、憨憨、UBits、家园和 1PTBA")
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
	var in SiteConfig
	warnings := []string{}
	var domain string
	if native {
		if json.Unmarshal(raw, &in) != nil {
			return in, warnings, bad("INVALID_SITE_ENTRY", "站点字段格式无效")
		}
	} else {
		var legacy moviePilotSiteImport
		if json.Unmarshal(raw, &legacy) != nil {
			return in, warnings, bad("INVALID_SITE_ENTRY", "MoviePilot 站点字段格式无效")
		}
		in = SiteConfig{Config: sites.Config{Name: legacy.Name, BaseURL: legacy.URL, Cookie: legacy.Cookie, APIKey: legacy.APIKey}, Enabled: legacy.Active == nil || *legacy.Active, DailyBudget: 600}
		domain = legacy.Domain
		if legacy.UA != "" || legacy.Proxy != 0 || legacy.Render != 0 || legacy.Timeout != 0 && legacy.Timeout != 25 {
			warnings = append(warnings, "原文件的 User-Agent、代理、浏览器渲染及超时选项未迁移，使用本应用连接方式")
		}
		if legacy.RSS != "" || legacy.Downloader != "" || legacy.Filter != "" || legacy.Public != 0 || legacy.Token != "" {
			warnings = append(warnings, "原文件的 RSS、下载器绑定、过滤规则、公开站点及 Token 选项未迁移；认证使用 API Key 或 Cookie")
		}
		if legacy.Interval != 0 || legacy.Count != 0 || legacy.Seconds != 0 || legacy.Priority != 0 {
			warnings = append(warnings, "原文件的优先级及流控选项未迁移；新站使用每日 600 次请求预算，已有站保留原预算")
		}
	}
	kind, canonical, actualDomain, err := siteImportOrigin(in.BaseURL)
	if err != nil {
		return in, warnings, err
	}
	if native && in.Kind != kind {
		return in, warnings, bad("SITE_ORIGIN_MISMATCH", "站点类型与地址不匹配，未导入")
	}
	if domain != "" && !strings.EqualFold(strings.TrimSpace(domain), actualDomain) {
		return in, warnings, bad("SITE_ORIGIN_MISMATCH", "MoviePilot 站点域名与地址不匹配，未导入")
	}
	in.Kind, in.BaseURL, in.ID = kind, canonical, ""
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 || invalidSiteImportText(in.Name, 256) {
		return in, warnings, bad("INVALID_SITE_NAME", "站点名称不能为空且最长 64 字，不能包含控制字符")
	}
	if invalidSiteImportText(in.Cookie, 64<<10) || invalidSiteImportText(in.APIKey, 8192) {
		return in, warnings, bad("INVALID_SITE_CREDENTIAL", "站点登录信息格式无效或过长")
	}
	if in.DailyBudget < 0 || in.DailyBudget > 10000 {
		return in, warnings, bad("INVALID_SITE_BUDGET", "每日请求预算须在 0 至 10000 之间")
	}
	return in, warnings, nil
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
	existingCount := len(config.Sites)
	seen := map[string]bool{}
	touched := map[string]bool{}
	for index, entry := range entries {
		result := siteImportResult{Index: index + 1, Name: fmt.Sprintf("第 %d 项", index+1), Status: "failed", Warnings: []string{}}
		in, warnings, err := decodeSiteImportEntry(entry, native)
		result.Warnings = warnings
		if err == nil {
			result.Name = siteImportName(in.Kind)
			identity := in.Kind + "|" + in.BaseURL
			if seen[identity] {
				err = bad("DUPLICATE_SITE_IMPORT", "文件中同一站点地址重复，后续条目未导入")
			} else {
				seen[identity] = true
			}
		}
		found := -1
		if err == nil {
			var sameKind []int
			for i, old := range config.Sites {
				if old.Kind != in.Kind {
					continue
				}
				if i < existingCount {
					sameKind = append(sameKind, i)
				}
				_, origin, _, originErr := siteImportOrigin(old.BaseURL)
				if originErr == nil && origin == in.BaseURL {
					if found >= 0 {
						err = bad("AMBIGUOUS_SITE", "已有多个相同站点地址，请先在站点管理中核对")
						break
					}
					found = i
				}
			}
			// Native exports can legitimately contain multiple origins using the
			// same adapter. Only MoviePilot migration may update a unique old
			// site's origin, and never an entry created earlier in this import.
			if !native && found < 0 && len(sameKind) == 1 {
				found = sameKind[0]
			} else if !native && found < 0 && len(sameKind) > 1 {
				err = bad("AMBIGUOUS_SITE", "已有多个该站点地址，无法确定更新对象，请先在站点管理中核对")
			}
		}
		if err == nil {
			if found >= 0 {
				old := config.Sites[found]
				in.ID = old.ID
				if touched[old.ID] {
					err = bad("DUPLICATE_SITE_IMPORT", "文件中多个条目对应同一已有站点，后续条目未导入")
				}
				if !native {
					in.DailyBudget = old.DailyBudget
				}
				// A changed origin must supply its own credential; do not move the
				// saved cookie/API key based only on a site name or adapter kind.
				_, origin, _, originErr := siteImportOrigin(old.BaseURL)
				if originErr == nil && origin == in.BaseURL {
					if in.Cookie == "" {
						in.Cookie = old.Cookie
					}
					if in.APIKey == "" {
						in.APIKey = old.APIKey
					}
				}
			} else {
				in.ID = id("site")
				if len(config.Sites) >= 32 {
					err = bad("SITE_LIMIT", "已达到 32 个站点的配置上限")
				}
			}
			if err == nil && in.Kind == "mteam" && in.APIKey == "" {
				err = bad("SITE_CREDENTIAL_REQUIRED", "馒头需要 API Key，Cookie 或 Token 不能替代")
			} else if err == nil && in.Kind != "mteam" && in.Cookie == "" {
				err = bad("SITE_CREDENTIAL_REQUIRED", "该站点需要 Cookie，请补充后再导入")
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
		}
		report.Results = append(report.Results, result)
	}
	if report.Imported > 0 {
		if err := a.saveSettings(ctx, config); err != nil {
			return siteImportReport{}, bad("SITE_IMPORT_FAILED", "站点配置保存失败，本次导入未生效")
		}
	}
	return report, nil
}

func (a *App) exportManagedSites(w http.ResponseWriter, r *http.Request) {
	configured := a.Settings().Sites
	if configured == nil {
		configured = []SiteConfig{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Disposition", `attachment; filename="light-media-sites-`+time.Now().In(shanghai()).Format("20060102")+`.json"`)
	_ = json.NewEncoder(w).Encode(siteExportDocument{Format: siteExportFormat, Version: 1, Sites: configured})
}
