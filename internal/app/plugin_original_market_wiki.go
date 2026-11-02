package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const originalPluginMarketWikiURL = "https://raw.githubusercontent.com/jxxghp/MoviePilot-Wiki/main/plugin.md"

var originalMarketWikiPath = regexp.MustCompile(`^/jxxghp/MoviePilot-Wiki/[^/]+/plugin\.md$`)
var originalMarketWikiRepository = regexp.MustCompile(`(?i)https?://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:\.git)?/?`)

func allowedOriginalMarketWikiURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "raw.githubusercontent.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && originalMarketWikiPath.MatchString(u.Path)
}

func normalizeOriginalWikiRepository(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return "https://github.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
}

func extractOriginalWikiRepositories(markdown string) []string {
	const start, end = "<!-- plugin-market-repos:start -->", "<!-- plugin-market-repos:end -->"
	a, b := strings.Index(markdown, start), strings.Index(markdown, end)
	if strings.Count(markdown, start) == 1 && strings.Count(markdown, end) == 1 && a < b {
		markdown = markdown[a+len(start) : b]
	}
	out := []string{}
	seen := map[string]bool{}
	for _, match := range originalMarketWikiRepository.FindAllString(markdown, -1) {
		repository := normalizeOriginalWikiRepository(match)
		key := strings.ToLower(repository)
		if repository != "" && !seen[key] {
			out = append(out, repository)
			seen[key] = true
		}
	}
	return out
}

func fetchOriginalMarketWiki(ctx context.Context, source string, client *http.Client) ([]string, error) {
	if !allowedOriginalMarketWikiURL(source) {
		return nil, errors.New("不支持的 Wiki 同步地址")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedOriginalMarketWikiURL(req.URL.String()) {
			return errors.New("Wiki 重定向超出固定文档源")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, errors.New("Wiki 请求无效")
	}
	req.Header.Set("Accept", "text/plain,*/*")
	res, err := copyClient.Do(req)
	if err != nil {
		return nil, errors.New("无法访问 Wiki 插件仓库清单")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("访问 Wiki 插件仓库清单失败，状态码：%d", res.StatusCode)
	}
	// Source consumes the complete fixed Wiki document, without an extra total
	// byte ceiling. Read in finite chunks with the existing request cancellation;
	// this is not an executable-artifact or generic HTTP budget change.
	var body bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			return nil, errors.New("Wiki 插件仓库清单读取失败")
		}
		n, readErr := res.Body.Read(chunk)
		if n > 0 {
			_, _ = body.Write(chunk[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, errors.New("Wiki 插件仓库清单读取失败")
		}
	}
	if ctx.Err() != nil {
		return nil, errors.New("Wiki 插件仓库清单读取失败")
	}
	repos := extractOriginalWikiRepositories(body.String())
	if len(repos) == 0 {
		return nil, errors.New("未在 Wiki 中识别到插件仓库地址")
	}
	return repos, nil
}

func (a *App) saveOriginalWikiRepositories(ctx context.Context, wiki []string) (map[string]any, error) {
	if a.pluginMarketOverride != nil {
		return nil, bad("PLUGIN_MARKET_ENV_OVERRIDE", "仓库由 PLUGIN_MARKET 环境变量设置，请修改启动环境后重启")
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	settings := a.Settings()
	// Keep the existing Go index URLs exactly: unlike Python repository roots,
	// a path within the URL is necessary to identify a Go artifact declaration.
	repos := append([]string{}, settings.PluginMarkets...)
	keys := map[string]bool{}
	key := func(value string) string {
		if normalized := normalizeOriginalWikiRepository(value); normalized != "" {
			return strings.ToLower(normalized)
		}
		return value
	}
	for _, repo := range repos {
		keys[key(repo)] = true
	}
	added := 0
	for _, repo := range wiki {
		if !keys[key(repo)] {
			repos = append(repos, repo)
			keys[key(repo)] = true
			added++
		}
	}
	settings.PluginMarkets = repos
	if err := a.saveOriginalPluginSettings(ctx, settings); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "repos": repos, "wiki_repos": wiki, "added_count": added, "total_count": len(repos), "message": "仓库清单已同步；原版 Python 仓库需提供 Go 制品后才能安装"}, nil
}

func (a *App) originalMarketWikiHTTP(w http.ResponseWriter, r *http.Request) (any, error) {
	var input struct {
		WikiURL string `json:"wiki_url"`
	}
	if r.ContentLength != 0 {
		if err := decodePluginBody(w, r, &input); err != nil {
			return nil, err
		}
	}
	source := input.WikiURL
	if source == "" {
		source = originalPluginMarketWikiURL
	}
	source = strings.TrimFunc(source, originalPluginVersionSpace)
	if !allowedOriginalMarketWikiURL(source) {
		return map[string]any{"success": false, "message": "不支持的 Wiki 同步地址"}, nil
	}
	client, err := a.originalPluginHTTPClientWithBase(30*time.Second, false, originalPluginSourceTLSBase())
	if err != nil {
		return map[string]any{"success": false, "message": "插件仓库代理配置无效"}, nil
	}
	defer client.CloseIdleConnections()
	repos, err := fetchOriginalMarketWiki(r.Context(), source, client)
	if err != nil {
		return map[string]any{"success": false, "message": err.Error()}, nil
	}
	return a.saveOriginalWikiRepositories(r.Context(), repos)
}
