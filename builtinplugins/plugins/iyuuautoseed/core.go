package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

type Service struct {
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	RuntimeKind string `json:"runtime_kind,omitempty"`
	Inactive    bool   `json:"inactive"`
}
type NativeTorrent struct {
	Fields map[string]any `json:"fields"`
}
type Site struct {
	Ref    string         `json:"ref"`
	Fields map[string]any `json:"fields"`
}
type AddSpec struct {
	Content             []byte
	Magnet              string
	SavePath            any
	Category            any
	Paused              bool
	Tags                []string
	SkipChecking        any
	SkipCheckingRaw     json.RawMessage
	IgnoreCategoryCheck bool
}
type AddReceipt struct {
	// QB SourceTruthy is the truth value of the wrapper's (bool, []IDs) tuple;
	// it remains true even when Accepted is false. TR uses its actual object.
	SourceTruthy bool
	Accepted     bool
	Hash         string
	Outcome      string
	ReceiptID    string
}
type EffectReceipt struct {
	Accepted  bool
	Outcome   string
	ReceiptID string
}
type TorrentContent struct {
	Bytes        []byte
	Magnet       string
	ErrorMessage string
}
type SeedAPI interface {
	SeedInfo(context.Context, []string) (APIResult, error)
	TorrentURL(context.Context, any) (any, any, error)
}

// Host is a fixed capability set. Service/Site refs must be minted and bound by
// the trusted host; this interface alone does not close the compiled SitesHelper
// dependency or claim that a remote side effect has a durable receipt.
type Host interface {
	Inventory(context.Context, string, any) ([]Service, error)
	Completed(context.Context, Service) ([]NativeTorrent, error)
	Torrents(context.Context, Service, []string) ([]NativeTorrent, error)
	AddPaused(context.Context, Service, AddSpec) (AddReceipt, error)
	FindByTag(context.Context, Service, string) (string, error)
	SetCategory(context.Context, Service, string, any) (EffectReceipt, error)
	Recheck(context.Context, Service, []string) (EffectReceipt, error)
	Start(context.Context, Service, []string) (EffectReceipt, error)
	Indexer(context.Context, string) (*Site, error)
	CheckSite(context.Context, string) (bool, error)
	DownloadURL(context.Context, map[string]any, Site, string) (string, error)
	Torrent(context.Context, Site, string) (TorrentContent, error)
	LoadHistory(context.Context, string) (any, error)
	SaveHistory(context.Context, string, any) error
	SaveConfig(context.Context, map[string]any) error
	Notify(context.Context, string, string) error
	RandomTag(context.Context, int) (string, error)
}
type Counters struct {
	Total     int `json:"total"`
	RealTotal int `json:"realtotal"`
	Success   int `json:"success"`
	Exist     int `json:"exist"`
	Fail      int `json:"fail"`
	Cached    int `json:"cached"`
}
type Attempt struct {
	Operation  string `json:"operation"`
	ServiceRef string `json:"service_ref"`
	Hash       string `json:"hash"`
	ReceiptID  string `json:"receipt_id"`
	Outcome    string `json:"outcome"`
	Accepted   bool   `json:"accepted"`
}
type State struct {
	Config         map[string]any      `json:"config"`
	Queue          map[string][]string `json:"queue"`
	AutoRechecked  map[string]bool     `json:"auto_rechecked"`
	RecheckRunning bool                `json:"recheck_running"`
	Counters       Counters            `json:"counters"`
	Attempts       []Attempt           `json:"attempts"`
}
type Core struct {
	runMu          sync.Mutex
	stopped        atomic.Bool
	config         Config
	host           Host
	api            SeedAPI
	queue          map[string][]string
	autoRechecked  map[string]bool
	recheckRunning bool
	counters       Counters
	attempts       []Attempt
	prepWarnings   map[string]*seedPreparationWarning
	prepOrder      []string
}

func NewCore(config Config, host Host, api SeedAPI) (*Core, error) {
	if config.values == nil || host == nil || api == nil {
		return nil, errors.New("IYUU config, host and helper required")
	}
	return newCore(config, host, api)
}

func newCore(config Config, host Host, api SeedAPI) (*Core, error) {
	snap, e := config.Snapshot()
	if e != nil {
		return nil, e
	}
	return &Core{
		config: Config{values: snap, skipRaw: append(json.RawMessage(nil), config.skipRaw...)}, host: host, api: api,
		queue: map[string][]string{}, autoRechecked: map[string]bool{}, attempts: []Attempt{},
	}, nil
}

func RestoreCore(state State, host Host, api SeedAPI) (*Core, error) {
	cfg, e := RestoreConfig(state.Config)
	if e != nil {
		return nil, e
	}
	c, e := NewCore(cfg, host, api)
	if e != nil {
		return nil, e
	}
	for ref, ids := range state.Queue {
		c.queue[ref] = append([]string{}, ids...)
	}
	for hash, checked := range state.AutoRechecked {
		c.autoRechecked[hash] = checked
	}
	c.recheckRunning = state.RecheckRunning
	c.counters = state.Counters
	c.attempts = append([]Attempt{}, state.Attempts...)
	return c, nil
}
func (c *Core) Stop() { c.stopped.Store(true) }
func (c *Core) Snapshot() (State, error) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	m, e := c.config.Snapshot()
	if e != nil {
		return State{}, e
	}
	q := map[string][]string{}
	for k, v := range c.queue {
		q[k] = append([]string{}, v...)
	}
	autoRechecked := make(map[string]bool, len(c.autoRechecked))
	for hash, checked := range c.autoRechecked {
		autoRechecked[hash] = checked
	}
	return State{
		Config: m, Queue: q, AutoRechecked: autoRechecked, RecheckRunning: c.recheckRunning,
		Counters: c.counters, Attempts: append([]Attempt{}, c.attempts...),
	}, nil
}
func (c *Core) active(ctx context.Context, logMode string) ([]Service, *Service, error) {
	if logMode == "auto" && !truth(c.config.values["auto_downloader"]) ||
		logMode == "services" && !truth(c.config.values["downloaders"]) {
		c.logInventory(ctx, nil, logMode)
		return nil, nil, nil
	}
	selection := c.config.values["downloaders"]
	if logMode == "auto" {
		selection = c.config.values["auto_downloader"]
	}
	inventory, e := c.host.Inventory(ctx, logMode, selection)
	if e != nil {
		return nil, nil, e
	}
	c.logInventory(ctx, inventory, logMode)
	selected := c.config.values["downloaders"]
	auto := c.config.values["auto_downloader"]
	out := []Service{}
	var dest *Service
	for _, s := range inventory {
		if s.Inactive {
			continue
		}
		if selectedName(selected, s.Name) {
			out = append(out, s)
		}
		if scalarEqual(auto, s.Name) {
			cp := s
			dest = &cp
		}
	}
	return out, dest, nil
}
func selectedName(v any, name string) bool {
	switch x := v.(type) {
	case []any:
		return containsScalar(x, name)
	case map[string]any:
		_, ok := x[name]
		return ok
	case string:
		return len([]rune(name)) == 1 && strings.Contains(x, name)
	default:
		return false
	}
}
func (c *Core) cacheHas(key string, value any) bool {
	return containsScalar(c.config.values[key].([]any), value)
}
func (c *Core) cacheAdd(key string, value any) error {
	v, e := AppendCache(c.config.values[key].([]any), value)
	if e == nil {
		c.config.values[key] = v
	}
	return e
}
func (c *Core) guard(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c.stopped.Load() {
		return errors.New("IYUU owner stopped")
	}
	return nil
}

func (c *Core) AutoSeed(ctx context.Context) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return c.autoSeed(ctx)
}

func (c *Core) autoSeed(ctx context.Context) error {
	if e := c.guard(ctx); e != nil {
		return e
	}
	c.beginPreparationWarnings()
	defer c.flushPreparationWarnings(ctx)
	services, _, e := c.active(ctx, "services")
	if e != nil {
		return e
	}
	if len(services) == 0 {
		return nil
	}
	sourceLog(ctx, 749, "开始辅种任务 ...")
	c.counters = Counters{}
	// Source re-evaluates the property for iteration after its preflight.
	services, _, e = c.active(ctx, "services")
	if e != nil {
		return e
	}
	var partialErr error
	for _, service := range services {
		sourceLog(ctx, 762, fmt.Sprintf("开始扫描下载器 %s ...", service.Name))
		torrents, e := c.host.Completed(ctx, service)
		if e != nil {
			return e
		}
		if len(torrents) == 0 {
			sourceLog(ctx, 768, fmt.Sprintf("下载器 %s 没有已完成种子", service.Name))
			continue
		}
		sourceLog(ctx, 766, fmt.Sprintf("下载器 %s 已完成种子数：%d", service.Name, len(torrents)))
		selected := []selectedTorrent{}
		completedHashes := make(map[string]bool, len(torrents))
		for _, torrent := range torrents {
			if e := c.guard(ctx); e != nil {
				if c.stopped.Load() {
					sourceLog(ctx, 775, "辅种服务停止")
				}
				return e
			}
			hash, e := nativeString(torrent, service.Kind, "hash", "hashString")
			if e != nil {
				return e
			}
			completedHashes[hash] = true
			if c.cacheHas("error_caches", hash) || c.cacheHas("permanent_error_caches", hash) {
				sourceLog(ctx, 781, fmt.Sprintf("种子 %s 辅种失败且已缓存，跳过 ...", hash))
				continue
			}
			save := nativeField(torrent, service.Kind, "save_path", "download_dir")
			noPaths, e := c.config.String("nopaths")
			if e != nil {
				return e
			}
			if noPaths != "" && truth(save) {
				s, ok := save.(string)
				if !ok {
					return sourceFault("TypeError", "os.path.normpath")
				}
				skip := false
				for _, n := range strings.Split(noPaths, "\n") {
					if strings.HasPrefix(posixNorm(s), posixNorm(n)) {
						sourceLog(ctx, 790, fmt.Sprintf("种子 %s 保存路径 %s 不需要辅种，跳过 ...", hash, s))
						skip = true
						break
					}
				}
				if skip {
					continue
				}
			}
			labels := nativeLabels(torrent, service.Kind)
			noLabels, e := c.config.String("nolabels")
			if e != nil {
				return e
			}
			skip := false
			if len(labels) > 0 && noLabels != "" {
				for _, label := range strings.Split(noLabels, ",") {
					if containsString(labels, label) {
						sourceLog(ctx, 802, fmt.Sprintf("种子 %s 含有不辅种标签 %s，跳过 ...", hash, label))
						skip = true
						break
					}
				}
			}
			if skip {
				continue
			}
			size, e := nativeSize(torrent, service.Kind)
			if e != nil {
				return e
			}
			if minSize := c.config.values["size"]; truth(minSize) {
				min, e := floatValue(minSize)
				if e != nil {
					return e
				}
				sizeGB := size / 1024 / 1024 / 1024
				if sizeGB < min {
					sourceLog(ctx, 810, fmt.Sprintf("种子 %s 大小:%.2fGB，小于设定 %sGB，跳过 ...", hash, sizeGB, pyString(minSize)))
					continue
				}
			}
			if service.Kind == "qbittorrent" {
				available, ok := torrent.Fields["availability"]
				if !ok {
					return sourceFault("AttributeError", "torrent.availability")
				}
				availability, e := floatValue(available)
				if e != nil {
					return e
				}
				if availability != -1 && availability < 1 {
					sourceLog(ctx, 815, fmt.Sprintf("种子 %s 下载不完整，跳过 ...", hash))
					continue
				}
				completed, ok := torrent.Fields["completed"]
				if !ok {
					return sourceFault("AttributeError", "torrent.completed")
				}
				if !scalarEqual(torrent.Fields["total_size"], completed) {
					sourceLog(ctx, 818, fmt.Sprintf("种子 %s 完成字节数与总体积不一致，跳过 ...", hash))
					continue
				}
			}
			var category any
			if truth(c.config.values["auto_category"]) && service.Kind == "qbittorrent" {
				category = torrent.Fields["category"]
			}
			category = or(category, c.config.values["categoryafterseed"])
			selected = append(selected, selectedTorrent{Hash: hash, SavePath: save, Category: category})
		}
		if len(selected) > 0 {
			sourceLog(ctx, 827, fmt.Sprintf("总共需要辅种的种子数：%d", len(selected)))
			var batchErr error
			for i := 0; i < len(selected); i += 200 {
				end := i + 200
				if end > len(selected) {
					end = len(selected)
				}
				batchErr = c.seedTorrents(ctx, selected[i:end], service, completedHashes)
				if batchErr != nil {
					break
				}
			}
			if batchErr != nil {
				var mutation *seedMutationError
				// An automatic destination may be shared by multiple source
				// downloaders. Never continue submitting writes to that destination.
				if !errors.As(batchErr, &mutation) || truth(c.config.values["auto_downloader"]) || c.guard(ctx) != nil {
					return errors.Join(partialErr, batchErr)
				}
				partialErr = errors.Join(partialErr, batchErr)
				logSeedWarning(ctx, "下载器 "+service.Name+" 的辅种添加或校验未确认，停止向此下载器提交，继续检查其他下载器")
				continue
			}
			sourceLog(ctx, 838, fmt.Sprintf("下载器 %s 辅种全部完成。", service.Name))
			if e := c.checkRecheck(ctx); e != nil {
				return e
			}
		} else {
			sourceLog(ctx, 841, "没有需要辅种的种子")
		}
	}
	cfg, e := c.config.Snapshot()
	if e != nil {
		return e
	}
	if e = c.host.SaveConfig(ctx, cfg); e != nil {
		return e
	}
	if truth(c.config.values["notify"]) && (c.counters.Success != 0 || c.counters.Fail != 0) {
		s := c.counters
		text := "服务器返回可辅种总数：" + itoa(s.Total) + "\n实际可辅种数：" + itoa(s.RealTotal) + "\n已存在：" + itoa(s.Exist) + "\n成功：" + itoa(s.Success) + "\n失败：" + itoa(s.Fail) + "\n" + itoa(s.Cached) + " 条失败记录已加入缓存"
		if e := c.host.Notify(ctx, "【IYUU自动辅种任务完成】", text); e != nil {
			return e
		}
	}
	if partialErr != nil {
		return partialErr
	}
	sourceLog(ctx, 858, "辅种任务执行完成")
	return nil
}

type selectedTorrent struct {
	Hash     string
	SavePath any
	Category any
}

func (c *Core) seedTorrents(ctx context.Context, items []selectedTorrent, service Service, completedHashes map[string]bool) error {
	if len(items) == 0 {
		return nil
	}
	sourceLog(ctx, 938, fmt.Sprintf("下载器 %s 开始查询辅种，数量：%d ...", service.Name, len(items)))
	hashes := make([]string, 0, len(items))
	byHash := map[string]selectedTorrent{}
	for _, item := range items {
		hashes = append(hashes, item.Hash)
		byHash[item.Hash] = item
	}
	r, e := c.api.SeedInfo(ctx, hashes)
	if e != nil {
		return e
	}
	seedList, ok := r.Data.(map[string]any)
	if !ok {
		msg := ""
		if r.Failure != nil {
			msg = r.Failure.sourceMessage
		}
		if truth(c.config.values["token"]) && msg == "请求缺少token" {
			sourceLog(ctx, 952, "IYUU辅种失败，疑似站点未绑定插件配置不完整，请先检查是否完成站点绑定！"+msg)
		} else {
			sourceLog(ctx, 954, "当前种子列表没有可辅种的站点："+msg)
		}
		return nil
	}
	sourceLog(ctx, 957, fmt.Sprintf("IYUU返回可辅种数：%d", len(seedList)))
	if len(seedList) > 0 && len(r.ObjectKeys) == 0 {
		return errors.New("IYUU ordered seed response required")
	}
	for _, currentHash := range r.ObjectKeys {
		raw := seedList[currentHash]
		if !truth(raw) {
			continue
		}
		info, ok := raw.(map[string]any)
		if !ok {
			return sourceFault("AttributeError", "seed_info.get")
		}
		seeds, ok := info["torrent"].([]any)
		if !ok {
			seeds = []any{info["torrent"]}
		}
		successes := []string{}
		var mutationErr error
		for _, rawSeed := range seeds {
			if !truth(rawSeed) {
				continue
			}
			seed, ok := rawSeed.(map[string]any)
			if !ok {
				continue
			}
			if !truth(seed["sid"]) || !truth(seed["info_hash"]) {
				continue
			}
			hash, ok := seed["info_hash"].(string)
			if !ok {
				return errors.New("unsupported non-string native seed hash")
			}
			if completedHashes[hash] {
				sourceLog(ctx, 977, hash+" 已在下载器中，跳过 ...")
				continue
			}
			if c.cacheHas("success_caches", hash) {
				sourceLog(ctx, 980, hash+" 已处理过辅种，跳过 ...")
				continue
			}
			if c.cacheHas("error_caches", hash) || c.cacheHas("permanent_error_caches", hash) {
				sourceLog(ctx, 983, "种子 "+hash+" 辅种失败且已缓存，跳过 ...")
				continue
			}
			dest := &service
			if truth(c.config.values["auto_downloader"]) {
				_, auto, e := c.active(ctx, "auto")
				if e != nil {
					return e
				}
				dest = auto
			}
			input := byHash[currentHash]
			success, e := c.downloadTorrent(ctx, seed, dest, input.SavePath, input.Category)
			if e != nil {
				var preparation *seedPreparationError
				if !errors.As(e, &preparation) || c.guard(ctx) != nil {
					mutationErr = e
					break
				}
				c.counters.Fail++
				c.recordPreparationWarning(service.Name, preparation)
				continue
			}
			if success {
				successes = append(successes, hash)
			}
		}
		if len(successes) > 0 {
			if e := c.saveHistory(ctx, currentHash, service.Name, successes); e != nil {
				var malformed historyShapeError
				if ctx.Err() != nil || !errors.As(e, &malformed) {
					return e
				}
				// MoviePilot prints this exception and continues the pass. The
				// Go worker's stdout belongs to the protocol, so print to stderr.
				fmt.Fprintln(os.Stderr, malformed)
			}
		}
		if mutationErr != nil {
			return mutationErr
		}
	}
	sourceLog(ctx, 1005, fmt.Sprintf("下载器 %s 辅种本批次完成", service.Name))
	return nil
}

// Preparation errors occur before submitting an add to a downloader. A failed
// candidate can be retried later without assuming whether a write took effect.
type seedPreparationError struct {
	stage string
	site  string
	hash  string
	cause error
}

func (e *seedPreparationError) Error() string { return e.stage }
func (e *seedPreparationError) Unwrap() error { return e.cause }

type seedMutationError struct{ cause error }

func (e *seedMutationError) Error() string { return "IYUU downloader add or verification failed" }
func (e *seedMutationError) Unwrap() error { return e.cause }

func (c *Core) downloadTorrent(ctx context.Context, seed map[string]any, sourceService *Service, save, category any) (_ bool, err error) {
	stage := "查询 IYUU 站点信息"
	hash := seed["info_hash"].(string)
	siteLabel := ""
	defer func() {
		if err == nil {
			return
		}
		if stage == "" {
			err = &seedMutationError{cause: err}
		} else {
			err = &seedPreparationError{stage: stage, site: siteLabel, hash: hash, cause: err}
		}
	}()
	c.counters.Total++
	siteURL, downloadPage, e := c.api.TorrentURL(ctx, seed["sid"])
	if e != nil {
		return false, e
	}
	failCached := func() (bool, error) {
		c.counters.Fail++
		c.counters.Cached++
		return false, c.cacheAdd("error_caches", hash)
	}
	if !truth(siteURL) || !truth(downloadPage) {
		return failCached()
	}
	stage = "解析辅种站点地址"
	sourceURL, ok := siteURL.(string)
	if !ok {
		return false, sourceFault("TypeError", "get_url_domain")
	}
	domain, e := SourceDomain(sourceURL)
	if e != nil {
		return false, e
	}
	siteLabel = domain
	stage = "读取站点配置"
	site, e := c.host.Indexer(ctx, domain)
	if e != nil {
		return false, e
	}
	if site == nil || !truth(site.Fields["url"]) {
		sourceLog(ctx, 1149, "没有维护种子对应的站点："+sourceURL)
		return false, nil
	}
	if name, ok := site.Fields["name"].(string); ok && strings.TrimSpace(name) != "" {
		siteLabel = strings.TrimSpace(name)
	}
	selected := c.config.values["sites"].([]any)
	if len(selected) > 0 && !containsScalar(selected, site.Fields["id"]) {
		sourceLog(ctx, 1152, "当前站点不在选择的辅种站点范围，跳过 ...")
		return false, nil
	}
	c.counters.RealTotal++
	stage = "选择目标下载器"
	if sourceService == nil {
		return false, sourceFault("AttributeError", "auto_service_info.instance")
	}
	service := *sourceService
	stage = "检查下载器是否已有种子"
	existing, e := c.host.Torrents(ctx, service, []string{hash})
	if e != nil {
		return false, e
	}
	if len(existing) > 0 {
		sourceLog(ctx, 1159, hash+" 已在下载器中，跳过 ...")
		c.counters.Exist++
		return false, nil
	}
	stage = "检查站点流控"
	limited, e := c.host.CheckSite(ctx, domain)
	if e != nil {
		return false, e
	}
	if limited {
		c.counters.Fail++
		return false, nil
	}
	stage = "解析种子下载地址"
	template, ok := downloadPage.(string)
	if !ok {
		return false, sourceFault("AttributeError", "download_page.replace")
	}
	url, e := c.host.DownloadURL(ctx, seed, *site, template)
	if e != nil {
		return false, e
	}
	if url == "" {
		return failCached()
	}
	if !strings.Contains(url, "hdsky.me") {
		if strings.Contains(url, "?") {
			url += "&https=1"
		} else {
			url += "?https=1"
		}
	}
	stage = "获取种子文件"
	content, e := c.host.Torrent(ctx, *site, url)
	if e != nil {
		return false, e
	}
	if len(content.Bytes) == 0 && content.Magnet == "" {
		c.counters.Fail++
		key := "permanent_error_caches"
		if strings.Contains(content.ErrorMessage, "无法打开链接") || strings.Contains(content.ErrorMessage, "触发站点流控") {
			key = "error_caches"
		}
		sourceLog(ctx, 1199, "下载种子文件失败："+url)
		return false, c.cacheAdd(key, hash)
	}
	// Once a downloader can receive an add, preserve the fatal error path.
	stage = ""
	sourceLog(ctx, 1202, "添加下载任务："+url+" ...")
	id, e := c.download(ctx, service, content, save, category, site.Fields["name"])
	if e != nil {
		return false, e
	}
	if id == "" {
		c.counters.Fail++
		return false, c.cacheAdd("error_caches", hash)
	}
	c.counters.Success++
	if service.Kind == "qbittorrent" {
		if truth(c.config.values["skipverify"]) {
			if truth(c.config.values["auto_start"]) {
				sourceLog(ctx, 1219, id+" 跳过校验，开启自动开始，注意观察种子的完整性")
				c.queueAdd(ctx, service, id)
			} else {
				sourceLog(ctx, 1223, id+" 跳过校验，请自行检查手动开始任务...")
			}
		} else {
			receipt, e := c.host.Recheck(ctx, service, []string{id})
			if e != nil {
				return false, e
			}
			if e = c.record("recheck", service, id, receipt); e != nil {
				return false, e
			}
			c.queueAdd(ctx, service, id)
		}
	} else {
		c.queueAdd(ctx, service, id)
	}
	sourceLog(ctx, 1231, "成功添加辅种下载，站点："+pyString(site.Fields["name"])+"，种子链接："+url)
	return true, c.cacheAdd("success_caches", hash)
}
func (c *Core) download(ctx context.Context, service Service, content TorrentContent, save, category, name any) (string, error) {
	labelString, e := c.config.String("labelsafterseed")
	if e != nil {
		return "", e
	}
	tags := strings.Split(labelString, ",")
	if truth(c.config.values["addhosttotag"]) {
		n, ok := name.(string)
		if !ok {
			return "", errors.New("unsupported non-string site tag")
		}
		tags = append(tags, n)
	}
	spec := AddSpec{Content: content.Bytes, Magnet: content.Magnet, SavePath: save, Category: category, Paused: true, Tags: tags}
	if service.Kind == "qbittorrent" {
		tag, e := c.host.RandomTag(ctx, 10)
		if e != nil {
			return "", e
		}
		spec.Tags = append(spec.Tags, tag)
		reuse := truth(c.config.values["reuse_save_path"])
		if reuse {
			spec.Category = nil
		}
		spec.SkipChecking = c.config.values["skipverify"]
		spec.SkipCheckingRaw = c.config.skipRaw
		spec.IgnoreCategoryCheck = !reuse
		r, e := c.host.AddPaused(ctx, service, spec)
		if e != nil && r.Outcome == "" {
			return "", e
		}
		if recorded := c.record("add", service, r.Hash, EffectReceipt{Accepted: r.Accepted, Outcome: r.Outcome, ReceiptID: r.ReceiptID}); recorded != nil {
			return "", recorded
		}
		if e != nil {
			return "", e
		}
		if !r.SourceTruthy {
			return "", nil
		}
		id, e := c.host.FindByTag(ctx, service, tag)
		if e != nil {
			return "", e
		}
		if id == "" {
			sourceLog(ctx, 1094, service.Name+" 下载任务添加成功，但获取任务信息失败！")
			return "", nil
		}
		if reuse && truth(category) {
			receipt, e := c.host.SetCategory(ctx, service, id, category)
			if e != nil {
				return "", e
			}
			if e = c.record("category", service, id, receipt); e != nil {
				return "", e
			}
		}
		return id, nil
	}
	if service.Kind == "transmission" {
		spec.Category = nil
		r, e := c.host.AddPaused(ctx, service, spec)
		if e != nil && r.Outcome == "" {
			return "", e
		}
		if recorded := c.record("add", service, r.Hash, EffectReceipt{Accepted: r.Accepted, Outcome: r.Outcome, ReceiptID: r.ReceiptID}); recorded != nil {
			return "", recorded
		}
		if e != nil {
			return "", e
		}
		if !r.SourceTruthy {
			return "", nil
		}
		if service.RuntimeKind == "qbittorrent" {
			return "", sourceFault("AttributeError", "added torrent.hashString")
		}
		return r.Hash, nil
	}
	sourceLog(ctx, 1113, "不支持的下载器："+service.Kind)
	return "", nil
}
func (c *Core) record(op string, service Service, hash string, r EffectReceipt) error {
	if r.Outcome != "confirmed" && r.Outcome != "rejected" && r.Outcome != "unknown" {
		return errors.New("effect receipt outcome required")
	}
	c.attempts = append(c.attempts, Attempt{Operation: op, ServiceRef: service.Ref, Hash: hash, ReceiptID: r.ReceiptID, Outcome: r.Outcome, Accepted: r.Accepted})
	if r.Outcome == "unknown" {
		return errors.New("IYUU remote effect outcome unknown; automatic continuation blocked")
	}
	return nil
}
func (c *Core) CheckRecheck(ctx context.Context) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return c.checkRecheck(ctx)
}
func (c *Core) checkRecheck(ctx context.Context) error {
	if len(c.queue) == 0 || c.recheckRunning {
		return nil
	}
	if e := c.guard(ctx); e != nil {
		return e
	}
	c.recheckRunning = true
	_, auto, e := c.active(ctx, "auto")
	if e != nil {
		return e
	}
	if auto != nil {
		_, auto, e = c.active(ctx, "auto")
		if e != nil {
			return e
		}
		if auto == nil {
			return sourceFault("AttributeError", "service.name")
		}
		if e = c.checkService(ctx, *auto); e != nil {
			return e
		}
		c.recheckRunning = false
		return nil
	}
	services, _, e := c.active(ctx, "services")
	if e != nil {
		return e
	}
	if len(services) == 0 {
		return nil
	}
	services, _, e = c.active(ctx, "services")
	if e != nil {
		return e
	}
	if len(services) == 0 {
		return sourceFault("AttributeError", "service_infos.values")
	}
	for _, service := range services {
		if e = c.checkService(ctx, service); e != nil {
			return e
		}
	}
	c.recheckRunning = false
	return nil
}
func (c *Core) checkService(ctx context.Context, service Service) error {
	ids := c.queue[service.Name]
	if len(ids) == 0 {
		return nil
	}
	sourceLog(ctx, 891, "开始检查下载器 "+service.Name+" 的校验任务 ...")
	torrents, e := c.host.Torrents(ctx, service, append([]string{}, ids...))
	if e != nil {
		return e
	}
	if torrents == nil {
		sourceLog(ctx, 922, "下载器 "+service.Name+" 查询校验任务失败，将在下次继续查询 ...")
		return nil
	}
	if len(torrents) == 0 {
		sourceLog(ctx, 925, "下载器 "+service.Name+" 中没有需要检查的校验任务，清空待处理列表 ...")
		c.queue[service.Name] = []string{}
		return nil
	}
	ready := []string{}
	needRecheck := []string{}
	for _, t := range torrents {
		hash, e := nativeString(t, service.Kind, "hash", "hashString")
		if e != nil {
			return e
		}
		if nativeCanSeed(t, service.Kind) {
			ready = append(ready, hash)
		} else if service.Kind == "qbittorrent" && (t.Fields["state"] == "stoppedDL" || t.Fields["state"] == "pausedDL") &&
			!truth(t.Fields["progress"]) && !c.autoRechecked[hash] {
			needRecheck = append(needRecheck, hash)
		}
	}
	if len(ready) > 0 {
		sourceLog(ctx, 911, fmt.Sprintf("共 %d 个任务校验完成，开始辅种 ...", len(ready)))
		r, e := c.host.Start(ctx, service, ready)
		if e != nil {
			return e
		}
		if e = c.record("start", service, strings.Join(ready, "|"), r); e != nil {
			return e
		}
		kept := []string{}
		for _, id := range ids {
			if !containsString(ready, id) && !containsString(kept, id) {
				kept = append(kept, id)
			}
		}
		c.queue[service.Name] = kept
	}
	if len(needRecheck) > 0 {
		sourceLog(ctx, 918, fmt.Sprintf("共 %d 个任务未校验，自动补发校验 ...", len(needRecheck)))
		for _, hash := range needRecheck {
			c.autoRechecked[hash] = true
		}
		r, e := c.host.Recheck(ctx, service, needRecheck)
		if e != nil {
			return e
		}
		return c.record("recheck", service, strings.Join(needRecheck, "|"), r)
	}
	return nil
}

type historyShapeError string

func (e historyShapeError) Error() string { return string(e) }

func (c *Core) saveHistory(ctx context.Context, currentHash, downloader string, success []string) error {
	history, e := c.host.LoadHistory(ctx, currentHash)
	if e != nil {
		return e
	}
	if !truth(history) {
		history = []any{}
	}
	rows, ok := history.([]any)
	if !ok {
		kind := formatType(history)
		if kind == "str" || kind == "dict" {
			return historyShapeError(fmt.Sprintf("'%s' object has no attribute 'append'", kind))
		}
		return historyShapeError(fmt.Sprintf("object of type '%s' has no len()", kind))
	}
	fresh := true
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || !truth(row) || !truth(row["downloader"]) {
			continue
		}
		if pyString(row["downloader"]) == downloader {
			old := or(row["torrents"], []any{})
			values, ok := old.([]any)
			if !ok {
				kind := formatType(old)
				if kind == "str" {
					return historyShapeError(`can only concatenate str (not "list") to str`)
				}
				return historyShapeError(fmt.Sprintf("unsupported operand type(s) for +: '%s' and 'list'", kind))
			}
			values = append([]any{}, values...)
			for _, s := range success {
				values = append(values, s)
			}
			dedup, e := historySet(values)
			if e != nil {
				return e
			}
			row["torrents"] = dedup
			fresh = false
			break
		}
	}
	if fresh {
		values := []any{}
		for _, s := range success {
			values = append(values, s)
		}
		dedup, e := historySet(values)
		if e != nil {
			return e
		}
		rows = append(rows, map[string]any{"downloader": downloader, "torrents": dedup})
	}
	return c.host.SaveHistory(ctx, currentHash, rows)
}

// Python list(set(...)) has process-randomized order. Preserve exact membership
// with deterministic first occurrence order, an explicit ordering difference.
func historySet(values []any) ([]any, error) {
	out := []any{}
	seen := map[string]bool{}
	for _, v := range values {
		k, e := scalarKey(v)
		if e != nil {
			return nil, historyShapeError(fmt.Sprintf("unhashable type: '%s'", formatType(v)))
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out, nil
}
func nativeField(t NativeTorrent, kind, qb, tr string) any {
	if kind == "qbittorrent" {
		return t.Fields[qb]
	}
	return t.Fields[tr]
}
func nativeString(t NativeTorrent, kind, qb, tr string) (string, error) {
	v := nativeField(t, kind, qb, tr)
	s, ok := v.(string)
	if !ok {
		return "", errors.New("unsupported non-string native torrent field")
	}
	return s, nil
}
func nativeLabels(t NativeTorrent, kind string) []string {
	if kind == "qbittorrent" {
		s, ok := t.Fields["tags"].(string)
		if !ok {
			return []string{}
		}
		out := []string{}
		for _, label := range strings.Split(s, ",") {
			out = append(out, strings.TrimFunc(label, func(r rune) bool { return unicode.IsSpace(r) || r >= '\x1c' && r <= '\x1f' }))
		}
		return out
	}
	out := []string{}
	if values, ok := t.Fields["labels"].([]any); ok {
		for _, v := range values {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func nativeSize(t NativeTorrent, kind string) (float64, error) {
	return floatValue(nativeField(t, kind, "total_size", "total_size"))
}
func floatValue(v any) (float64, error) {
	switch x := v.(type) {
	case json.Number:
		f, e := x.Float64()
		if e != nil {
			return 0, e
		}
		return f, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, sourceFault("TypeError", "native numeric division")
	}
}
func nativeCanSeed(t NativeTorrent, kind string) bool {
	if kind == "qbittorrent" {
		return t.Fields["state"] == "pausedUP" || t.Fields["state"] == "stoppedUP"
	}
	stopped, ok := t.Fields["status_stopped"].(bool)
	return ok && stopped && scalarEqual(t.Fields["percent_done"], json.Number("1"))
}
func containsString(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}
func posixNorm(s string) string {
	p := path.Clean(s)
	if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
		p = "/" + p
	}
	return p
}
