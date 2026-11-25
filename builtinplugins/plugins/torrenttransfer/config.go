package torrenttransfer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var configKeys = []string{"enabled", "onlyonce", "cron", "notify", "nolabels", "includelabels", "includecategory", "frompath", "topath", "fromdownloader", "todownloader", "deletesource", "deleteduplicate", "fromtorrentpath", "nopaths", "autostart", "skipverify", "transferemptylabel", "add_torrent_tags", "remainoldcat", "remainoldtag"}

type Config struct {
	values  map[string]any
	tags    []string
	skipRaw json.RawMessage
}

func NewConfig() Config {
	m := map[string]any{}
	for _, k := range configKeys {
		m[k] = nil
	}
	for _, k := range []string{"enabled", "onlyonce", "notify", "deletesource", "deleteduplicate", "autostart", "skipverify", "transferemptylabel", "remainoldcat", "remainoldtag"} {
		m[k] = false
	}
	return Config{values: m, tags: []string{}}
}
func RestoreConfig(m map[string]any) (Config, error) {
	x, e := cloneMap(m)
	if e != nil {
		return Config{}, e
	}
	c := Config{values: x, tags: []string{}}
	if truth(c.values["add_torrent_tags"]) {
		s, e := asString(c.values["add_torrent_tags"])
		if e != nil {
			return c, e
		}
		c.tags = splitTags(s)
	}
	return c, nil
}
func (c Config) Snapshot() (map[string]any, error) { return cloneMap(c.values) }
func (c Config) State() bool {
	for _, k := range []string{"enabled", "cron", "fromdownloader", "todownloader", "fromtorrentpath"} {
		if !truth(c.values[k]) {
			return false
		}
	}
	return true
}
func (c Config) string(k string) (string, error) {
	if c.values[k] == nil {
		return "", nil
	}
	return asString(c.values[k])
}
func (c Config) flag(k string) bool { return truth(c.values[k]) }
func (c Config) Services(timezone string) ([]Job, error) {
	if !c.State() {
		return []Job{}, nil
	}
	cron, e := c.string("cron")
	if e != nil {
		return nil, e
	}
	if _, e = ParseCrontab(cron, timezone); e != nil {
		return nil, e
	}
	return []Job{{ID: "TorrentTransfer", Name: "转移做种服务", Kind: "cron", Cron: cron}}, nil
}

// Init drives exactly the same configuration implementation as the SDK
// object's init handler. Pure-source tests inject ordinary Host dependencies.
func (c *Config) Init(ctx context.Context, input map[string]any, h Host) (InitResult, error) {
	return c.initialize(ctx, input, h, nil, nil, time.Now)
}
func (c *Config) initialize(ctx context.Context, input any, h Host, stop func() error, changed func(Config), now func() time.Time) (InitResult, error) {
	r := InitResult{StopExisting: true, Jobs: []Job{}, ConfigWrites: []map[string]any{}}
	if c.values == nil {
		*c = NewConfig()
	}
	publish := func() {
		if changed != nil {
			changed(*c)
		}
	}
	values, ok := input.(map[string]any)
	if truth(input) {
		if !ok {
			return r, errors.New("configuration has no get method")
		}
		// These last two assignments follow strip/split in the source. If its
		// operand raises, previous labels and retention flags remain on the object.
		for _, k := range configKeys[:len(configKeys)-2] {
			c.values[k] = values[k]
		}
		if !truth(c.values["add_torrent_tags"]) {
			c.values["add_torrent_tags"] = ""
		}
		publish()
		tags := []string{}
		if truth(c.values["add_torrent_tags"]) {
			s, e := asString(c.values["add_torrent_tags"])
			if e != nil {
				return r, e
			}
			tags = splitTags(s)
		}
		c.tags = tags
		c.values["remainoldcat"] = values["remainoldcat"]
		c.values["remainoldtag"] = values["remainoldtag"]
		publish()
	}
	if stop != nil {
		if e := stop(); e != nil {
			return r, e
		}
	}
	if !c.State() && !c.flag("onlyonce") {
		return r, nil
	}
	valid, e := c.Validate(ctx, h)
	if e != nil {
		return r, e
	}
	if !valid {
		c.values["enabled"] = false
		c.values["onlyonce"] = false
		publish()
		if values == nil {
			return r, errors.New("configuration does not support item assignment")
		}
		values, e = cloneMap(values)
		if e != nil {
			return r, e
		}
		values["enabled"] = false
		values["onlyonce"] = false
		r.ConfigWrites = append(r.ConfigWrites, values)
		return r, h.SaveConfig(ctx, c.saveConfigValue(values))
	}
	if _, e = privateTimezone(); e != nil {
		return r, e
	}
	r.Scheduler = true
	r.StartAt = now()
	if c.flag("autostart") {
		r.Jobs = append(r.Jobs, Job{ID: "check_recheck", Kind: "interval", Interval: 30 * time.Second})
	}
	if c.flag("onlyonce") {
		logAt(ctx, h, 124, "INFO", "转移做种服务启动，立即运行一次")
		r.SourceOnlyOnce = true
		r.Jobs = append(r.Jobs, Job{ID: "transfer", Kind: "date", Delay: 3 * time.Second})
		c.values["onlyonce"] = false
		publish()
		if values == nil {
			return r, errors.New("configuration does not support item assignment")
		}
		values, e = cloneMap(values)
		if e != nil {
			return r, e
		}
		values["onlyonce"] = false
		r.ConfigWrites = append(r.ConfigWrites, values)
		if e = h.SaveConfig(ctx, c.saveConfigValue(values)); e != nil {
			return r, e
		}
	}
	return r, nil
}
func (c Config) Validate(ctx context.Context, h Host) (bool, error) {
	if c.flag("fromtorrentpath") {
		p, e := c.string("fromtorrentpath")
		if e != nil {
			return false, e
		}
		ok, e := h.Exists(ctx, p)
		if e != nil {
			return false, e
		}
		if !ok {
			msg := "源下载器种子文件保存路径不存在：" + p
			logAt(ctx, h, 605, "ERROR", msg)
			return false, h.SystemMessage(ctx, "自动转移做种", msg)
		}
	}
	// The original check compares two None values too. Do not make either
	// downloader implicitly select the application's default instance.
	if pyEqual(c.values["fromdownloader"], c.values["todownloader"]) {
		msg := "源下载器和目的下载器不能相同"
		logAt(ctx, h, 609, "ERROR", msg)
		return false, h.SystemMessage(ctx, "自动转移做种", msg)
	}
	return true, nil
}

func splitTags(s string) []string { return strings.Split(pyStrip(s), ",") }

func (c Config) saveConfigValue(values map[string]any) map[string]any {
	if len(c.skipRaw) > 0 {
		values["skipverify"] = append(json.RawMessage{}, c.skipRaw...)
	}
	return values
}
