package app

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
)

// The source's first RequestUtils attempt and its explicit settings.PROXY
// retry have separate clients. Neither is a process-global transport mutation.
func (a *App) cloudflareNetworkClients() (*http.Client, *http.Client, bool, func(), error) {
	config, err := a.proxyConfig()
	if err != nil {
		return nil, nil, false, nil, err
	}
	direct := a.pluginCloudflare.client
	var base *http.Transport
	owned := []*http.Client{}
	if direct == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
		direct = &http.Client{Transport: base, Timeout: 60 * time.Second}
		owned = append(owned, direct)
	} else {
		base, _ = direct.Transport.(*http.Transport)
	}
	proxied := direct
	if base != nil && (config.HTTP != "" || config.HTTPS != "") {
		route, e := config.Transport(base)
		if e != nil {
			return nil, nil, false, nil, e
		}
		copy := *direct
		copy.Transport = route
		proxied = &copy
		owned = append(owned, proxied)
	}
	if a.pluginCloudflare.proxyClient != nil {
		proxied = a.pluginCloudflare.proxyClient
	}
	close := func() {
		for _, c := range owned {
			c.CloseIdleConnections()
		}
	}
	return direct, proxied, config.HTTPS != "", close, nil
}

func cloudflareDownloadRoute(req cf.InstallRequest, httpsProxy bool) (string, bool, error) {
	if httpsProxy || req.Platform.OS == "windows" {
		return req.URL, true, nil
	}
	mirror, present := os.LookupEnv("PROXY_HOST")
	if !present {
		mirror = "https://ghfast.top"
	}
	u, err := url.Parse(mirror)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false, errors.New("CloudflareSpeedTest mirror invalid")
	}
	return strings.TrimRight(mirror, "/") + "/" + req.URL, false, nil
}
