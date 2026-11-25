package cloudflarespeedtest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

const (
	customHostsCapability = "cloudflare_speed.custom_hosts"
	saveHostsCapability   = "cloudflare_speed.save_hosts"
	reloadHostsCapability = "cloudflare_speed.reload_hosts"
	platformCapability    = "cloudflare_speed.platform"
	existsCapability      = "cloudflare_speed.exists"
	ensureCapability      = "cloudflare_speed.ensure"
	resetCapability       = "cloudflare_speed.reset"
	latestCapability      = "cloudflare_speed.latest"
	downloadCapability    = "cloudflare_speed.download"
	extractCapability     = "cloudflare_speed.extract"
	removeCapability      = "cloudflare_speed.remove"
	runCapability         = "cloudflare_speed.run"
	readResultCapability  = "cloudflare_speed.read_result"
)

type callbackKey struct{}

type standardHost struct{ plugin *Plugin }

func activeCaller(ctx context.Context) sdk.HostCaller {
	caller, _ := ctx.Value(callbackKey{}).(sdk.HostCaller)
	return caller
}

func (h standardHost) Guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if activeCaller(ctx) == nil {
		return errors.New("CloudflareSpeedTest requires an active callback")
	}
	return nil
}

func (h standardHost) call(ctx context.Context, method string, fields any, result any) error {
	if err := h.Guard(ctx); err != nil {
		return err
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	reply, err := activeCaller(ctx)(ctx, method, raw)
	if err != nil || result == nil {
		return err
	}
	return json.Unmarshal(reply, result)
}

func (h standardHost) effect(ctx context.Context, method string, fields any) error {
	var accepted bool
	if err := h.call(ctx, method, fields, &accepted); err != nil {
		return err
	}
	if !accepted {
		return errors.New("CloudflareSpeedTest host did not confirm " + method)
	}
	return nil
}

func (h standardHost) CustomHosts(ctx context.Context) (Object, error) {
	var config Object
	err := h.call(ctx, customHostsCapability, struct{}{}, &config)
	return config, err
}
func (h standardHost) SaveCustomHosts(ctx context.Context, config Object) error {
	return h.effect(ctx, saveHostsCapability, Object{"config": config})
}
func (h standardHost) ReloadCustomHosts(ctx context.Context) error {
	return h.effect(ctx, reloadHostsCapability, struct{}{})
}
func (h standardHost) SaveConfig(ctx context.Context, config Object) error {
	_, err := (sdk.Base{Host: activeCaller(ctx)}).UpdateConfig(ctx, config)
	return err
}
func (h standardHost) PostMessage(ctx context.Context, message Message) error {
	fields := Object{"title": message.Title}
	if message.Type == "SiteMessage" {
		fields["mtype"] = "站点"
	}
	if message.Text != "" {
		fields["text"] = message.Text
	}
	if message.Channel != nil {
		fields["channel"] = message.Channel
	}
	if message.User != nil {
		fields["userid"] = message.User
	}
	_, err := (sdk.Base{Host: activeCaller(ctx)}).PostMessage(ctx, fields)
	return err
}
func (h standardHost) SystemMessage(ctx context.Context, title, text string) error {
	return h.call(ctx, sdk.SystemMessagePutCapability, Object{"title": title, "message": text}, nil)
}
func (h standardHost) ScheduleOnce(ctx context.Context, due time.Time) error {
	return h.plugin.scheduleOnce(ctx, due)
}
func (h standardHost) StartOnce(ctx context.Context) error { return h.plugin.startOnce(ctx) }
func (h standardHost) StopOnce(ctx context.Context) error  { return h.plugin.stopOnce(ctx, false) }
func (h standardHost) Now() time.Time                      { return time.Now() }
func (h standardHost) Log(ctx context.Context, level, message string) {
	if len(message) > sdk.LogMessageLimit {
		message = message[:sdk.LogMessageLimit]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	_ = (sdk.Logger{Host: activeCaller(ctx)}).Log(ctx, sdk.LogLevel(strings.ToUpper(level)), message)
}

func (h standardHost) Platform(ctx context.Context) (Platform, error) {
	var result Platform
	err := h.call(ctx, platformCapability, struct{}{}, &result)
	if err == nil && (result.OS != "linux" || result.Machine != "x86_64" && result.Machine != "aarch64") {
		err = errors.New("CloudflareSpeedTest supports Linux amd64 and arm64")
	}
	return result, err
}
func (h standardHost) Exists(ctx context.Context, ref FileRef) (bool, error) {
	var result bool
	err := h.call(ctx, existsCapability, ref, &result)
	return result, err
}
func (h standardHost) Ensure(ctx context.Context) error {
	return h.effect(ctx, ensureCapability, struct{}{})
}
func (h standardHost) Reset(ctx context.Context) error {
	return h.effect(ctx, resetCapability, struct{}{})
}
func (h standardHost) Latest(ctx context.Context, proxy bool) (ReleaseResponse, error) {
	var result ReleaseResponse
	err := h.call(ctx, latestCapability, Object{"proxy": proxy}, &result)
	return result, err
}
func (h standardHost) Download(ctx context.Context, request InstallRequest) error {
	return h.effect(ctx, downloadCapability, request)
}
func (h standardHost) Extract(ctx context.Context, request InstallRequest) error {
	var accepted *bool
	if err := h.call(ctx, extractCapability, request, &accepted); err != nil {
		return err
	}
	if accepted == nil {
		return errors.New("CloudflareSpeedTest host did not report extraction result")
	}
	if !*accepted {
		return &ExtractionError{}
	}
	return nil
}
func (h standardHost) Remove(ctx context.Context, name string) error {
	return h.effect(ctx, removeCapability, Object{"name": name})
}
func (h standardHost) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	var result RunResult
	err := h.call(ctx, runCapability, spec, &result)
	return result, err
}
func (h standardHost) ReadResult(ctx context.Context) ([]byte, error) {
	var result struct {
		Content []byte `json:"content"`
	}
	err := h.call(ctx, readResultCapability, struct{}{}, &result)
	return result.Content, err
}
