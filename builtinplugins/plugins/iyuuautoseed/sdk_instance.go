package iyuuautoseed

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

type sdkInstanceHost struct {
	caller  sdk.HostCaller
	skipRaw json.RawMessage
}

func (h sdkInstanceHost) LoadHistory(ctx context.Context, hash string) (any, error) {
	raw, err := (sdk.Base{Host: h.caller}).GetData(ctx, &hash)
	if err != nil {
		return nil, err
	}
	return decodeJSON(raw)
}

func (h sdkInstanceHost) SaveHistory(ctx context.Context, hash string, history any) error {
	return (sdk.Base{Host: h.caller}).SaveData(ctx, hash, history)
}

func (h sdkInstanceHost) SaveConfig(ctx context.Context, config map[string]any) error {
	_, err := (sdk.Base{Host: h.caller}).UpdateConfig(ctx, withRawSkip(config, h.skipRaw))
	return err
}

func (h sdkInstanceHost) Notify(ctx context.Context, title, text string) error {
	_, err := (sdk.Base{Host: h.caller}).PostMessage(ctx, map[string]any{"mtype": "站点", "title": title, "text": text})
	return err
}

func (h sdkInstanceHost) RandomTag(ctx context.Context, size int) (string, error) {
	const alphabet = "ABCDEFGHIGKLMNOPQRSTUVWXYZabcdefghigklmnopqrstuvwxyz0123456789"
	limit := big.NewInt(int64(len(alphabet)))
	tag := make([]byte, size)
	for i := range tag {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		tag[i] = alphabet[index.Int64()]
	}
	return string(tag), nil
}

var sourcePrivateURL = regexp.MustCompile(`(?i)(https?://|magnet:\?|//)[^\s<>"']+`)

func bindSourceLog(ctx context.Context, caller sdk.HostCaller, token string) context.Context {
	logger := sdk.Logger{Host: caller}
	dedup := newSourceLogDedup()
	ctx = context.WithValue(ctx, sourceLogDedupKey{}, dedup)
	return WithSourceLogSink(ctx, func(ctx context.Context, entry SourceLog) {
		message := entry.Message
		// These diagnostics contain untrusted API messages or private torrent
		// links. Omit their remote payload even when it lacks a URL scheme.
		switch entry.Line {
		case 952:
			message = "IYUU辅种失败，疑似站点未绑定插件配置不完整，请先检查是否完成站点绑定！"
		case 954:
			message = "当前种子列表没有可辅种的站点"
		case 1431, 1528:
			message = "找到种子下载链接"
		case 1495:
			message = "种子下载地址模板无效"
		case 1199:
			message = "下载种子文件失败"
		case 1202:
			message = "添加下载任务"
		case 1231:
			message, _, _ = strings.Cut(message, "，种子链接：")
		}
		if token != "" {
			for _, secret := range []string{token, url.QueryEscape(token), url.PathEscape(token)} {
				message = strings.ReplaceAll(message, secret, "[已隐藏]")
			}
		}
		message = sourcePrivateURL.ReplaceAllString(message, "[链接已隐藏]")
		message = strings.ToValidUTF8(message, "�")
		if len(message) > sdk.LogMessageLimit {
			message = message[:sdk.LogMessageLimit]
			for !utf8.ValidString(message) {
				message = message[:len(message)-1]
			}
		}
		if entry.Level == "WARN" || entry.Level == "ERROR" {
			if dedup.record(entry.Level, message) {
				return
			}
		}
		_ = logger.Log(ctx, sdk.LogLevel(entry.Level), message)
	})
}
