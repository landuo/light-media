package autobackup

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/schedule"
	py "light-media/internal/releasemeta/legacyanime"
)

var configKeys = []string{"enabled", "cron", "cnt", "notify", "onlyonce", "back_path", "webdav_enabled", "webdav_hostname", "webdav_login", "webdav_password", "webdav_digest_auth", "webdav_max_count", "webdav_notify", "webdav_disable_check"}

type Plugin struct {
	mu        sync.Mutex
	config    Object
	deps      Dependencies
	dav       DAV
	scheduler Scheduler
}

func New(d Dependencies) *Plugin {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Timezone == "" {
		d.Timezone = "UTC"
	}
	if d.LocalTimezone == "" {
		d.LocalTimezone = localTimezoneName()
	}
	if d.DataPath == nil {
		d.DataPath = func() string { return "" }
	}
	return &Plugin{deps: d, config: Object{"enabled": false, "cron": nil, "cnt": nil, "notify": false, "onlyonce": false, "back_path": nil, "webdav_enabled": false, "webdav_hostname": nil, "webdav_login": nil, "webdav_password": nil, "webdav_digest_auth": false, "webdav_max_count": nil, "webdav_notify": false, "webdav_disable_check": false}}
}
func (p *Plugin) log(ctx context.Context, level, message string) {
	if p.deps.Log != nil {
		p.deps.Log(ctx, level, message)
	}
}
func (p *Plugin) Config() Object { p.mu.Lock(); defer p.mu.Unlock(); return ownedMap(p.config) }
func (p *Plugin) GetState() any  { p.mu.Lock(); defer p.mu.Unlock(); return p.config["enabled"] }
func (p *Plugin) Stop(ctx context.Context) {
	p.mu.Lock()
	s := p.scheduler
	p.mu.Unlock()
	if s == nil {
		return
	}
	if e := s.Clear(ctx); e != nil {
		p.log(ctx, "error", "退出插件失败："+e.Error())
		return
	}
	if s.Running() {
		if e := s.Shutdown(ctx); e != nil {
			p.log(ctx, "error", "退出插件失败："+e.Error())
			return
		}
	}
	p.mu.Lock()
	if p.scheduler == s {
		p.scheduler = nil
	}
	p.mu.Unlock()
}
func (p *Plugin) Init(ctx context.Context, config any) error {
	p.Stop(ctx)
	if truth(config) {
		m, ok := config.(map[string]any)
		if !ok {
			return source("AttributeError", "config.get")
		}
		p.mu.Lock()
		for _, k := range configKeys {
			v, ok := m[k]
			if !ok && strings.HasPrefix(k, "webdav_") && (k == "webdav_enabled" || k == "webdav_digest_auth" || k == "webdav_notify" || k == "webdav_disable_check") {
				v = false
			}
			p.config[k] = v
		}
		enabled := truth(p.config["webdav_enabled"])
		p.mu.Unlock()
		if enabled {
			p.initDAV(ctx)
		}
	}
	c := p.Config()
	if !truth(c["onlyonce"]) {
		return nil
	}
	if p.deps.NewScheduler == nil {
		return source("RuntimeError", "scheduler dependency")
	}
	s, e := p.deps.NewScheduler(ctx, p.deps.Timezone)
	if e != nil {
		return e
	}
	p.mu.Lock()
	p.scheduler = s
	p.mu.Unlock()
	p.log(ctx, "info", "自动备份服务启动，立即运行一次")
	zone, e := time.LoadLocation(p.deps.Timezone)
	if e != nil {
		return source("UnknownTimeZoneError", "timezone")
	}
	if e = s.AddDate(ctx, p.deps.Now().In(zone).Add(3*time.Second), "自动备份"); e != nil {
		return e
	}
	p.mu.Lock()
	p.config["onlyonce"] = false
	c = ownedMap(p.config)
	p.mu.Unlock()
	if p.deps.SaveConfig == nil {
		return source("RuntimeError", "config dependency")
	}
	if e = p.deps.SaveConfig(ctx, c); e != nil {
		return e
	}
	n, e := s.Jobs(ctx)
	if e != nil {
		return e
	}
	if n > 0 {
		if e = s.Print(ctx); e != nil {
			return e
		}
		return s.Start(ctx)
	}
	return nil
}
func (p *Plugin) initDAV(ctx context.Context) bool {
	c := p.Config()
	if !truth(c["webdav_hostname"]) || !truth(c["webdav_login"]) || !truth(c["webdav_password"]) || p.deps.NewDAV == nil {
		p.log(ctx, "warning", "WebDAV配置不完整，请检查服务器地址、登录名和密码")
		return false
	}
	options := Object{"webdav_hostname": c["webdav_hostname"], "webdav_login": c["webdav_login"], "webdav_password": c["webdav_password"], "webdav_digest_auth": c["webdav_digest_auth"]}
	if truth(c["webdav_disable_check"]) {
		options["disable_check"] = true
	}
	dav, e := p.deps.NewDAV(ctx, options)
	if e != nil {
		p.log(ctx, "error", "WebDAV客户端初始化失败: "+e.Error())
		return false
	}
	p.mu.Lock()
	p.dav = dav
	p.mu.Unlock()
	p.log(ctx, "info", "WebDAV客户端初始化成功")
	return true
}
func (p *Plugin) ServiceTrigger() (*schedule.Spec, error) {
	c := p.Config()
	if !truth(c["enabled"]) || !truth(c["cron"]) {
		return nil, nil
	}
	s, ok := c["cron"].(string)
	if !ok {
		return nil, source("AttributeError", "cron.split")
	}
	if _, e := schedule.ParseCrontab(s, p.deps.LocalTimezone); e != nil {
		return nil, source("ValueError", "cron")
	}
	return &schedule.Spec{Kind: "cron", Timezone: p.deps.LocalTimezone, Cron: s}, nil
}
func (p *Plugin) GetService() (any, error) {
	c := p.Config()
	if !truth(c["enabled"]) || !truth(c["cron"]) {
		return nil, nil
	}
	s, ok := c["cron"].(string)
	if !ok {
		return nil, source("AttributeError", "cron.split")
	}
	if _, e := schedule.ParseCrontab(s, p.deps.LocalTimezone); e != nil {
		return nil, source("ValueError", "cron")
	}
	parts := py.Fields(s)
	return []any{Object{"id": "AutoBackup", "name": "自动备份定时服务", "trigger": Object{"cron": s, "fields": []string{"*", parts[3], parts[2], "*", parts[4], parts[1], parts[0], "0"}}, "func": "__backup", "kwargs": Object{}}}, nil
}
func (p *Plugin) APIBackup(ctx context.Context, key, expected string) (any, error) {
	if key != expected {
		return response(false, "API密钥错误"), nil
	}
	r, e := p.Backup(ctx)
	if e != nil {
		return nil, e
	}
	return []any{r.Success, r.Message}, nil
}
func (p *Plugin) BackupResponse(ctx context.Context) (Response, error) {
	r, e := p.Backup(ctx)
	return response(r.Success, r.Message), e
}
func (p *Plugin) Backup(ctx context.Context) (Result, error) {
	p.log(ctx, "info", "当前时间 "+p.deps.Now().Format("2006-01-02 15:04:05")+" 开始备份")
	c := p.Config()
	dest := p.deps.DataPath()
	if truth(c["back_path"]) {
		var e error
		dest, e = stringPath(c["back_path"])
		if e != nil {
			return Result{}, e
		}
	}
	if p.deps.BackupFile == nil {
		return Result{}, source("RuntimeError", "backup dependency")
	}
	zip, e := p.deps.BackupFile(ctx, dest)
	if e != nil {
		return Result{}, e
	}
	success := zip != nil && *zip != ""
	msg := "创建备份失败"
	if success {
		msg = "备份完成 备份文件 " + *zip
		p.log(ctx, "info", msg)
	} else {
		p.log(ctx, "error", msg)
	}
	bkCount := 0
	delCount := big.NewInt(0)
	if truth(c["cnt"]) {
		if p.deps.ListBackups == nil {
			return Result{}, source("RuntimeError", "listing dependency")
		}
		files, e := p.deps.ListBackups(ctx, dest)
		if e != nil {
			return Result{}, e
		}
		sort.SliceStable(files, func(i, j int) bool { return files[i].Created < files[j].Created })
		bkCount = len(files)
		cnt, e := integer(c["cnt"])
		if e != nil {
			return Result{}, e
		}
		delCount.Sub(big.NewInt(int64(bkCount)), cnt)
		if delCount.Sign() > 0 {
			p.log(ctx, "info", fmt.Sprintf("获取到 %s 路径下备份文件数量 %d 保留数量 %s 需要删除备份文件数量 %s", dest, bkCount, cnt.String(), delCount.String()))
			for i := 0; big.NewInt(int64(i)).Cmp(delCount) < 0; i++ {
				if i >= len(files) {
					return Result{}, source("IndexError", "retention files")
				}
				f := files[i]
				switch f.Kind {
				case "file":
					if p.deps.RemoveFile == nil {
						return Result{}, source("RuntimeError", "remove dependency")
					}
					e = p.deps.RemoveFile(ctx, f.Path)
				case "dir":
					if p.deps.RemoveTree == nil {
						return Result{}, source("RuntimeError", "remove dependency")
					}
					e = p.deps.RemoveTree(ctx, f.Path)
				}
				if e != nil {
					return Result{}, e
				}
				p.log(ctx, "debug", "删除备份 "+f.Path+" 成功")
			}
		} else {
			p.log(ctx, "info", fmt.Sprintf("获取到 %s 路径下备份文件数量 %d 保留数量 %s 无需删除", dest, bkCount, cnt.String()))
		}
	}
	davSuccess, davMessage := true, ""
	if truth(c["webdav_enabled"]) && success {
		davSuccess, davMessage = p.upload(ctx, *zip)
		if davSuccess && truth(c["webdav_max_count"]) {
			p.cleanDAV(ctx, c["webdav_max_count"])
		}
	}
	if truth(c["notify"]) || (truth(c["webdav_enabled"]) && truth(c["webdav_notify"])) {
		s := "失败"
		if success {
			s = "成功"
		}
		notice := "创建备份" + s + "\n"
		if success {
			remaining := new(big.Int).Sub(big.NewInt(int64(bkCount)), delCount)
			notice += fmt.Sprintf("清理备份数量 %s\n剩余备份数量 %s\n", delCount.String(), remaining.String())
		}
		if truth(c["webdav_enabled"]) {
			s = "失败"
			if davSuccess {
				s = "成功"
			}
			notice += "\nWebDAV上传" + s
			if davMessage != "" {
				notice += "\n" + davMessage
			}
		}
		if p.deps.PostMessage == nil {
			return Result{}, source("RuntimeError", "notice dependency")
		}
		if e = p.deps.PostMessage(ctx, Object{"mtype": "SiteMessage", "title": "【自动备份任务完成】", "text": notice}); e != nil {
			return Result{}, e
		}
	}
	if davMessage != "" {
		msg += " " + davMessage
	}
	return Result{success && davSuccess, msg}, nil
}
func (p *Plugin) upload(ctx context.Context, local string) (bool, string) {
	p.log(ctx, "info", "开始上传备份文件到WebDAV服务器")
	p.mu.Lock()
	dav := p.dav
	p.mu.Unlock()
	if dav == nil {
		if !p.initDAV(ctx) {
			return false, "WebDAV客户端初始化失败"
		}
		p.mu.Lock()
		dav = p.dav
		p.mu.Unlock()
	}
	if _, e := dav.List(ctx, "/"); e != nil {
		p.log(ctx, "error", "连接到WebDAV服务器失败: "+e.Error())
		return false, "连接到WebDAV服务器失败"
	}
	p.log(ctx, "info", "成功连接到WebDAV服务器")
	name := path.Base(local)
	c := p.Config()
	host, ok := c["webdav_hostname"].(string)
	if !ok {
		return false, "上传到WebDAV服务器失败: invalid hostname"
	}
	base, e := url.Parse(host + "/")
	if e != nil {
		return false, "上传到WebDAV服务器失败: " + e.Error()
	}
	ref, _ := url.Parse(name)
	remote := base.ResolveReference(ref).String()
	p.log(ctx, "info", "远程备份路径为："+remote)
	message := ""
	e = dav.Upload(ctx, name, local, func() error {
		exists, e := dav.Check(ctx, remote)
		if e == nil && exists {
			p.log(ctx, "info", "上传完成，远程备份路径："+remote)
			message = "WebDAV备份验证成功"
		} else {
			if e != nil {
				p.log(ctx, "warning", "检查文件存在性时出错: "+e.Error()+"，但上传可能已完成")
			} else {
				p.log(ctx, "error", "上传完成，但远程备份路径没有检测到备份文件，请检查备份路径是否正确，远程备份路径："+remote)
			}
			message = "WebDAV备份上传完成但验证失败"
		}
		return nil
	})
	if e != nil {
		p.log(ctx, "error", "上传到WebDAV服务器失败: "+e.Error())
		return false, "上传到WebDAV服务器失败: " + e.Error()
	}
	return true, message
}

var backupPattern = regexp.MustCompile(`^bk_` + py.DecimalClass() + `{14}\.zip`)

func (p *Plugin) cleanDAV(ctx context.Context, count any) {
	p.mu.Lock()
	dav := p.dav
	p.mu.Unlock()
	if !truth(count) || dav == nil {
		return
	}
	files, e := dav.List(ctx, "/")
	if e != nil {
		p.log(ctx, "error", "获取WebDAV文件列表失败: "+e.Error())
		return
	}
	type dated struct {
		name string
		t    time.Time
	}
	var list []dated
	for _, s := range files {
		if !backupPattern.MatchString(s) {
			continue
		}
		d, e := parseBackupDate(s)
		if e != nil {
			p.log(ctx, "error", "获取WebDAV文件列表失败: "+e.Error())
			return
		}
		list = append(list, dated{s, d})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].t.Before(list[j].t) })
	n, e := integer(count)
	if e != nil {
		p.log(ctx, "error", "获取WebDAV文件列表失败: "+e.Error())
		return
	}
	if n.Cmp(big.NewInt(int64(len(list)))) >= 0 {
		p.log(ctx, "info", fmt.Sprintf("WebDAV上备份文件数量为 %d，符合最大保留数 %s，不需删除文件", len(list), text(count)))
		return
	}
	p.log(ctx, "info", fmt.Sprintf("WebDAV上备份文件数量为 %d，超出最大保留数 %s，需删除 %s 个备份文件", len(list), text(count), new(big.Int).Sub(big.NewInt(int64(len(list))), n).String()))
	end := new(big.Int).Neg(n)
	if end.Sign() < 0 {
		end.Add(end, big.NewInt(int64(len(list))))
	}
	if end.Sign() < 0 {
		end.SetInt64(0)
	}
	if end.Cmp(big.NewInt(int64(len(list)))) > 0 {
		end.SetInt64(int64(len(list)))
	}
	for i := 0; i < int(end.Int64()); i++ {
		remote := "/" + list[i].name
		if e := dav.Delete(ctx, remote); e != nil {
			p.log(ctx, "error", "删除WebDAV文件 "+remote+" 失败: "+e.Error())
		} else {
			p.log(ctx, "info", "WebDAV上的备份文件 "+remote+" 已删除")
		}
	}
}

var dateDigits = py.DecimalClass()
var backupDatePattern = regexp.MustCompile(`^bk_(` + dateDigits + `{4})(1[0-2]|0[1-9]|[1-9])(3[0-1]|[0-2]` + dateDigits + `|` + dateDigits + `)([0-1]` + dateDigits + `|2[0-3]|` + dateDigits + `)([0-5]` + dateDigits + `|` + dateDigits + `)(6[0-1]|[0-5]` + dateDigits + `|` + dateDigits + `)\.zip$`)

func parseBackupDate(s string) (time.Time, error) {
	m := backupDatePattern.FindStringSubmatch(s)
	if len(m) != 7 {
		return time.Time{}, source("ValueError", "strptime")
	}
	v := make([]int, 6)
	for i := range v {
		n, e := py.Integer(m[i+1])
		if e != nil || !n.IsInt64() {
			return time.Time{}, source("ValueError", "strptime")
		}
		v[i] = int(n.Int64())
	}
	if v[0] < 1 || v[0] > 9999 || v[1] < 1 || v[1] > 12 || v[2] < 1 || v[3] > 23 || v[4] > 59 || v[5] > 59 {
		return time.Time{}, source("ValueError", "strptime")
	}
	d := time.Date(v[0], time.Month(v[1]), v[2], v[3], v[4], v[5], 0, time.UTC)
	if d.Day() != v[2] || int(d.Month()) != v[1] {
		return time.Time{}, source("ValueError", "strptime")
	}
	return d, nil
}
