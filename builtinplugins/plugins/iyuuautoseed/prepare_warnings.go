package iyuuautoseed

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// A preparation failure repeats for every candidate of the same site and
// stage. Report those together once per run: the operator still sees exactly
// which seeds failed, without one warning line per seed flooding the log.
type seedPreparationWarning struct {
	downloader, site, stage, reason string
	hashes                          []string
	count                           int
}

const seedWarningHashSample = 5

func (c *Core) beginPreparationWarnings() {
	c.prepWarnings = map[string]*seedPreparationWarning{}
	c.prepOrder = nil
}

func (c *Core) recordPreparationWarning(downloader string, failure *seedPreparationError) {
	if failure == nil {
		return
	}
	site := failure.site
	if site == "" {
		site = "未知站点"
	}
	reason := seedFailureReason(failure.cause)
	key := downloader + "\x00" + site + "\x00" + failure.stage + "\x00" + reason
	if c.prepWarnings == nil {
		c.prepWarnings = map[string]*seedPreparationWarning{}
	}
	warning := c.prepWarnings[key]
	if warning == nil {
		warning = &seedPreparationWarning{downloader: downloader, site: site, stage: failure.stage, reason: reason}
		c.prepWarnings[key] = warning
		c.prepOrder = append(c.prepOrder, key)
	}
	warning.count++
	hash := failure.hash
	if len(hash) > 12 {
		hash = hash[:12]
	}
	if hash != "" && len(warning.hashes) < seedWarningHashSample && !containsString(warning.hashes, hash) {
		warning.hashes = append(warning.hashes, hash)
	}
}

func (c *Core) flushPreparationWarnings(ctx context.Context) {
	if len(c.prepOrder) == 0 {
		return
	}
	// The run may already be cancelled or out of time; the aggregate is a
	// diagnostic and must still reach the host log sink.
	logCtx := context.WithoutCancel(ctx)
	for _, key := range c.prepOrder {
		warning := c.prepWarnings[key]
		if warning == nil {
			continue
		}
		message := "下载器 " + warning.downloader + "：站点 " + warning.site + " 的辅种候选在「" + warning.stage + "」失败 " + strconv.Itoa(warning.count) + " 颗"
		if len(warning.hashes) > 0 {
			message += "（" + strings.Join(warning.hashes, "、")
			if warning.count > len(warning.hashes) {
				message += " 等"
			}
			message += "）"
		}
		if warning.reason != "" {
			message += "，原因：" + warning.reason
		}
		logSeedWarning(logCtx, message)
	}
	c.beginPreparationWarnings()
}

// seedFailureReason keeps the host error class readable without exposing site
// URLs, torrent links or credentials. Arbitrary error text is never logged:
// only fixed source messages and look-alike host error codes are surfaced.
var seedFailureReasonPattern = regexp.MustCompile(`[A-Z][A-Z0-9_]{4,63}`)

var seedFailureReasons = []struct{ match, label string }{
	{"invalid source indexer response", "站点索引返回格式无效"},
	{"invalid source indexer fields", "站点索引字段无效"},
	{"invalid site domain", "站点域名为空或无效"},
	{"source site changed during indexer lookup", "站点在读取配置期间被修改"},
	{"host resource requires an active host caller", "插件未获得宿主调用"},
	{"source site changed", "站点在读取配置期间被修改"},
	{"context deadline exceeded", "宿主调用超时"},
	{"context canceled", "宿主调用被取消"},
	{"plugin_capability_denied", "插件缺少站点读取权限"},
	{"plugin_host_contract_unsupported", "当前宿主不支持该插件契约"},
	{"site_changed", "站点配置在读取期间发生变化"},
	{"invalid_plugin_value", "宿主拒绝站点读取参数"},
	{"app_stopping", "宿主正在停止"},
}

func seedFailureReason(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	for _, known := range seedFailureReasons {
		if strings.Contains(lower, known.match) {
			return known.label
		}
	}
	// Upper-case identifier tokens are host error codes and carry no payload.
	if code := seedFailureReasonPattern.FindString(text); code != "" {
		return "宿主错误 " + code
	}
	return "宿主返回未识别错误（详情已隐藏）"
}
