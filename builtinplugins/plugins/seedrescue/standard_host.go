package seedrescue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/torrenttransfer"
)

const (
	iyuuCapability             = "seed_rescue.iyuu"
	serviceCapability          = "seed_rescue.service"
	siteCapability             = "seed_rescue.site"
	indexerCapability          = "seed_rescue.indexer"
	optionsCapability          = "seed_rescue.options"
	rssCapability              = "seed_rescue.rss"
	seedInfoCapability         = "seed_rescue.seed_info"
	torrentURLCapability       = "seed_rescue.torrent_url"
	searchCapability           = "seed_rescue.search"
	detailCapability           = "seed_rescue.detail"
	downloadMetadataCapability = "seed_rescue.download_metadata"
	queryTorrentsCapability    = "seed_rescue.query_torrents"
	downloadingCapability      = "seed_rescue.downloading_torrents"
	addTorrentCapability       = "seed_rescue.add_torrent"
)

type standardHost struct{ zone *time.Location }
type invocationHostKey struct{}

type invocationHost struct {
	caller   sdk.HostCaller
	iyuuOnce sync.Once
	iyuu     struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	}
	iyuuError error
}

func invocation(ctx context.Context) *invocationHost {
	host, _ := ctx.Value(invocationHostKey{}).(*invocationHost)
	return host
}

func (h *standardHost) Guard(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if call := invocation(ctx); call == nil || call.caller == nil {
		return errors.New("SeedRescue requires an active host invocation")
	}
	return nil
}

func (h *standardHost) Now() time.Time { return time.Now().In(h.zone) }

func (h *standardHost) call(ctx context.Context, method string, fields any, result any) error {
	if err := h.Guard(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	raw, err := invocation(ctx).caller(ctx, method, data)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(result)
}

func (h *standardHost) LoadData(ctx context.Context, key string) (json.RawMessage, error) {
	if err := h.Guard(ctx); err != nil {
		return nil, err
	}
	return (sdk.Base{Host: invocation(ctx).caller}).GetData(ctx, &key)
}
func (h *standardHost) SaveData(ctx context.Context, key string, value json.RawMessage) error {
	if err := h.Guard(ctx); err != nil {
		return err
	}
	return (sdk.Base{Host: invocation(ctx).caller}).SaveData(ctx, key, value)
}
func (h *standardHost) UpdateConfig(ctx context.Context, fields Object) error {
	if err := h.Guard(ctx); err != nil {
		return err
	}
	_, err := (sdk.Base{Host: invocation(ctx).caller}).UpdateConfig(ctx, fields)
	return err
}

func (h *standardHost) iyuuStatus(ctx context.Context) *invocationHost {
	call := invocation(ctx)
	call.iyuuOnce.Do(func() {
		call.iyuuError = h.call(ctx, iyuuCapability, struct{}{}, &call.iyuu)
	})
	return call
}
func (h *standardHost) IYUUAvailable(ctx context.Context) bool {
	return h.iyuuStatus(ctx).iyuu.Available
}
func (h *standardHost) IYUUEnabled(ctx context.Context) bool { return h.iyuuStatus(ctx).iyuu.Enabled }
func (h *standardHost) Service(ctx context.Context, name string) (*Service, error) {
	var out *Service
	err := h.call(ctx, serviceCapability, Object{"name": name}, &out)
	return out, err
}
func (h *standardHost) Site(ctx context.Context, id int) (Object, error) {
	var out Object
	err := h.call(ctx, siteCapability, Object{"id": id}, &out)
	return out, err
}
func (h *standardHost) Indexer(ctx context.Context, domain string) (Object, error) {
	var out Object
	err := h.call(ctx, indexerCapability, Object{"domain": domain}, &out)
	return out, err
}
func (h *standardHost) Options(ctx context.Context) (Object, error) {
	var out Object
	err := h.call(ctx, optionsCapability, struct{}{}, &out)
	return out, err
}
func (h *standardHost) RSS(ctx context.Context, url string, site Object) ([]Object, bool, error) {
	var out struct {
		Items    []Object `json:"items"`
		Complete bool     `json:"complete"`
	}
	err := h.call(ctx, rssCapability, Object{"url": url, "site": site}, &out)
	return out.Items, out.Complete, err
}
func (h *standardHost) SeedInfo(ctx context.Context, hashes []string) (any, string, error) {
	var out struct {
		Payload any    `json:"payload"`
		Message string `json:"message"`
	}
	err := h.call(ctx, seedInfoCapability, Object{"hashes": hashes}, &out)
	return out.Payload, out.Message, err
}
func (h *standardHost) TorrentURL(ctx context.Context, sid any) (string, string, error) {
	var out struct {
		URL  string `json:"url"`
		Page string `json:"page"`
	}
	err := h.call(ctx, torrentURLCapability, Object{"sid": sid}, &out)
	return out.URL, out.Page, err
}
func (h *standardHost) Search(ctx context.Context, domain, keyword string, page int) ([]Object, error) {
	var out []Object
	err := h.call(ctx, searchCapability, Object{"domain": domain, "keyword": keyword, "page": page}, &out)
	return out, err
}
func (h *standardHost) Detail(ctx context.Context, domain string, site Object, torrentID string) (DetailResult, error) {
	var out DetailResult
	err := h.call(ctx, detailCapability, Object{"domain": domain, "site": site, "torrent_id": torrentID}, &out)
	return out, err
}
func (h *standardHost) DownloadMetadata(ctx context.Context, torrent Object) ([]byte, string, error) {
	var out struct {
		Content []byte `json:"content"`
		Message string `json:"message"`
	}
	err := h.call(ctx, downloadMetadataCapability, Object{"torrent": torrent}, &out)
	return out.Content, out.Message, err
}
func (h *standardHost) QueryTorrents(ctx context.Context, name string, hashes []string) ([]Object, error) {
	var out []Object
	err := h.call(ctx, queryTorrentsCapability, Object{"name": name, "hashes": hashes}, &out)
	return out, err
}
func (h *standardHost) DownloadingTorrents(ctx context.Context, name, tag string) ([]Object, error) {
	var out []Object
	err := h.call(ctx, downloadingCapability, Object{"name": name, "tag": tag}, &out)
	return out, err
}
func (h *standardHost) AddTorrent(ctx context.Context, request AddRequest) (bool, error) {
	var out bool
	err := h.call(ctx, addTorrentCapability, Object{"downloader": request.Downloader, "content": request.Content,
		"save_path": request.SavePath, "category": request.Category, "tags": request.Tags}, &out)
	return out, err
}
func (h *standardHost) PostMessage(ctx context.Context, fields Object) error {
	if err := h.Guard(ctx); err != nil {
		return err
	}
	if fields["mtype"] == "SiteMessage" {
		fields["mtype"] = "站点"
	}
	_, err := (sdk.Base{Host: invocation(ctx).caller}).PostMessage(ctx, fields)
	return err
}
func (h *standardHost) Log(ctx context.Context, level, message string) {
	if h.Guard(ctx) != nil {
		return
	}
	if len(message) > sdk.LogMessageLimit {
		message = message[:sdk.LogMessageLimit]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	_ = (sdk.Logger{Host: invocation(ctx).caller}).Log(ctx, sdk.LogLevel(strings.ToUpper(level)), message)
}
func (h *standardHost) ValidateCron(cron string) error {
	_, err := torrenttransfer.ParseCrontab(cron, "UTC")
	return err
}
