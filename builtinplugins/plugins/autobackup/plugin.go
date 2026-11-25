package autobackup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
	_ "time/tzdata"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/removelink"
	"light-media/builtinplugins/plugins/torrenttransfer"
)

func backupAPI() sdk.HTTPAPI {
	return sdk.HTTPAPI{
		Key: "api_backup", Path: "/backup", Methods: []string{http.MethodGet}, Auth: "apikey",
		Query: []sdk.HTTPParameter{{Name: "apikey", Kind: "api_key", Required: true}},
	}
}

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "AutoBackup", Name: "自动备份",
		Description: "自动备份数据和配置文件。", Icon: "Time_machine_B.png", Version: "1.0.0",
		Author: "thsrite", AuthorURL: "https://github.com/thsrite", PluginOrder: 17,
		Runtime: "go-executable", Entry: "auto-backup", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPrivateScheduler: true, ManagedPublicServices: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_render_mode", "get_service", "get_api", "get_page", "get_command",
			"backup", "private_once", "http", "close", "stop_service", "stop",
		},
		Capabilities: []string{
			sdk.BaseGetDataPath, sdk.BaseUpdateConfig, sdk.BackgroundSubmitCapability, sdk.LocalBackupCapability,
			sdk.LogWriteCapability, sdk.PostMessageCapability,
		},
		APIs:           []sdk.HTTPAPI{backupAPI()},
		RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type configuration struct {
	OnlyOnce any `json:"onlyonce"`
	Cron     any `json:"cron"`
	Enabled  any `json:"enabled"`
	Count    any `json:"cnt"`
	Notify   any `json:"notify"`
	Path     any `json:"back_path"`
}

type onceJob struct {
	id      uint64
	due     time.Time
	stop    chan struct{}
	done    chan struct{}
	claimed bool
	active  bool
}

type Plugin struct {
	initMu sync.Mutex
	mu     sync.Mutex
	config configuration
	next   uint64
	once   *onceJob
}

func NewPlugin() *Plugin {
	return &Plugin{config: configuration{OnlyOnce: false, Enabled: false, Notify: false}}
}

func (p *Plugin) snapshot() configuration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.initialize(ctx, raw, caller)
	case "get_state":
		return json.Marshal(p.snapshot().Enabled)
	case "get_form":
		return backupForm(ctx, caller)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_service":
		return p.services()
	case "get_api":
		return json.Marshal([]sdk.HTTPAPI{backupAPI()})
	case "backup":
		success, message, err := p.backup(ctx, caller)
		if err != nil {
			return nil, err
		}
		return sdk.ServiceResultTuple(success, message)
	case "private_once":
		return nil, p.runOnce(ctx, raw, caller)
	case "http":
		return p.httpBackup(ctx, raw, caller)
	case "stop_service", "stop":
		p.initMu.Lock()
		defer p.initMu.Unlock()
		return nil, p.stopOnce(ctx)
	case "get_page", "get_command", "close":
		return nil, nil
	default:
		return nil, errors.New("unsupported AutoBackup method")
	}
}

func (p *Plugin) services() (json.RawMessage, error) {
	config := p.snapshot()
	if !removelink.Truth(config.Enabled) || !removelink.Truth(config.Cron) {
		return nil, nil
	}
	cron, ok := config.Cron.(string)
	if !ok {
		return nil, errors.New("invalid AutoBackup crontab expression")
	}
	// Source constructs CronTrigger in get_service, before the host creates a
	// public job. The actual clock uses the host's local zone, as tzlocal does.
	if _, err := torrenttransfer.ParseCrontab(cron, "UTC"); err != nil {
		return nil, err
	}
	return json.Marshal([]any{map[string]any{
		"id": "AutoBackup", "name": "自动备份定时服务", "handler": "backup",
		"trigger": map[string]any{"kind": "cron", "cron": cron, "local_timezone": true},
		"kwargs":  map[string]any{},
	}})
}

func (p *Plugin) initialize(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	p.initMu.Lock()
	defer p.initMu.Unlock()
	if err := p.stopOnce(ctx); err != nil {
		return err
	}
	if len(raw) != 0 {
		value, err := removelink.Decode(raw)
		if err != nil {
			return err
		}
		if removelink.Truth(value) {
			fields, ok := value.(map[string]any)
			if !ok {
				return errors.New("AutoBackup configuration has no get method")
			}
			p.mu.Lock()
			p.config = configuration{
				OnlyOnce: fields["onlyonce"], Cron: fields["cron"], Enabled: fields["enabled"],
				Count: fields["cnt"], Notify: fields["notify"], Path: fields["back_path"],
			}
			p.mu.Unlock()
		}
	}
	if !removelink.Truth(p.snapshot().OnlyOnce) {
		return nil
	}
	zone, configured := os.LookupEnv("TZ")
	if !configured {
		zone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(zone); zone == "" || zone == "Local" || err != nil {
		return errors.New("invalid AutoBackup private scheduler timezone")
	}
	_ = (sdk.Logger{Host: caller}).Log(ctx, sdk.LogInfo, "自动备份服务启动，立即运行一次")
	p.mu.Lock()
	p.next++
	job := &onceJob{id: p.next, due: time.Now().Add(3 * time.Second), stop: make(chan struct{}), done: make(chan struct{})}
	p.once = job
	p.config.OnlyOnce = false
	config := p.config
	p.mu.Unlock()
	if _, err := (sdk.Base{Host: caller}).UpdateConfig(ctx, config); err != nil {
		return err
	}
	return (sdk.Manager{Host: caller}).SubmitPrivateScheduler(ctx, "private_once", job.id)
}

func (p *Plugin) stopOnce(ctx context.Context) error {
	p.mu.Lock()
	job := p.once
	if job == nil {
		p.mu.Unlock()
		return nil
	}
	p.once = nil
	close(job.stop)
	active := job.active
	p.mu.Unlock()
	if active {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *Plugin) runOnce(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	var id uint64
	if err := json.Unmarshal(raw, &id); err != nil {
		return err
	}
	p.mu.Lock()
	job := p.once
	if job == nil || job.id != id || job.claimed {
		p.mu.Unlock()
		return nil
	}
	job.claimed = true
	p.mu.Unlock()
	defer close(job.done)
	timer := time.NewTimer(time.Until(job.due))
	defer timer.Stop()
	select {
	case <-job.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	p.mu.Lock()
	if p.once != job || time.Since(job.due) > time.Second {
		p.mu.Unlock()
		return nil
	}
	job.active = true
	p.mu.Unlock()
	_, _, err := p.backup(ctx, caller)
	return err
}

func (p *Plugin) httpBackup(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	var request sdk.HTTPRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if request.Key != "api_backup" || request.Path != "/backup" || request.Method != http.MethodGet {
		return nil, errors.New("unsupported AutoBackup API request")
	}
	if !request.CredentialMatches["apikey"] {
		const message = "API密钥错误"
		return sdk.JSONHTTPResponse(http.StatusOK, map[string]any{
			"success": false, "message": message, "message_i18n": message, "data": map[string]any{},
		})
	}
	success, message, err := p.backup(ctx, caller)
	if err != nil {
		return nil, err
	}
	return sdk.JSONHTTPResponse(http.StatusOK, []any{success, message})
}

func (p *Plugin) backup(ctx context.Context, caller sdk.HostCaller) (bool, string, error) {
	logger := sdk.Logger{Host: caller}
	_ = logger.Log(ctx, sdk.LogInfo, "当前时间 "+time.Now().Format(time.DateTime)+" 开始备份")
	config := p.snapshot()
	request := sdk.LocalBackupRequestV1{}
	if removelink.Truth(config.Path) {
		path, ok := config.Path.(string)
		if !ok {
			return false, "", errors.New("备份保存路径必须是字符串")
		}
		request.Path = path
	}
	if removelink.Truth(config.Count) {
		count, err := json.Marshal(config.Count)
		if err != nil {
			return false, "", err
		}
		request.Retain = count
	}
	result, err := (sdk.LocalBackup{Host: caller}).Run(ctx, request)
	if err != nil {
		return false, "", err
	}
	message := "创建备份失败"
	level := sdk.LogError
	if result.ArchiveCreated {
		message = "备份完成 备份文件 " + result.ArchivePath
		level = sdk.LogInfo
	}
	_ = logger.Log(ctx, level, message)
	if removelink.Truth(config.Notify) {
		text := "创建备份失败\n"
		if result.ArchiveCreated {
			text = fmt.Sprintf("创建备份成功\n清理备份数量 %s\n剩余备份数量 %s\n", result.DeleteCount, result.RemainingCount)
		}
		if _, err := (sdk.Base{Host: caller}).PostMessage(ctx, map[string]any{
			"mtype": "站点", "title": "【自动备份任务完成】", "text": text,
		}); err != nil {
			return false, "", err
		}
	}
	return result.ArchiveCreated, message, nil
}
