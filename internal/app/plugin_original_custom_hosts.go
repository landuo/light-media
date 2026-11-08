package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/releasemeta/legacyanime"
)

const originalCustomHostsCapability = "custom_hosts.apply"

type originalCustomHostsReloadKey struct{}

// The plugin and Base config callbacks share pluginMu and the Base cache lock.
type originalCustomHostsCall struct {
	config      json.RawMessage
	fingerprint originalBaseConfigFingerprint
	init        *originalInitializationCall
	used        bool
	expected    *originalCustomHostsSaved
}

type originalCustomHostsSaved struct {
	Enabled  bool     `json:"enabled"`
	Hosts    string   `json:"hosts"`
	ErrHosts []string `json:"err_hosts"`
}

func originalCustomHostsInstallation(in PluginInstallation) bool {
	return in.PluginID == "CustomHosts" && in.Version == "1.2.2"
}

func originalCustomHostsReload(raw json.RawMessage, instance string) bool {
	var event plugins.MutableEvent
	var data struct {
		Instance string `json:"plugin_id"`
	}
	return json.Unmarshal(raw, &event) == nil && event.Type == "plugin.reload" &&
		json.Unmarshal(event.Data, &data) == nil && data.Instance == instance
}

func (p *originalPluginInstance) originalCustomHostsCallFor(ctx context.Context, phase string, init *originalInitializationCall) *originalCustomHostsCall {
	if !originalCustomHostsInstallation(p.installation) {
		return nil
	}
	if phase == "init" && init != nil && originalLiveInitialization(ctx, p) == init {
		return &originalCustomHostsCall{config: copyOriginalBaseJSON(init.config), init: init}
	}
	if phase == "reload_event" && ctx.Value(originalCustomHostsReloadKey{}) == p && p.originalPublicServiceScopeAllowed(ctx) {
		return &originalCustomHostsCall{}
	}
	return nil
}

func (a *App) originalCustomHostsCurrent(ctx context.Context, scope originalBaseCallScope) error {
	in := scope.installation
	current, err := a.pluginStore.Installation(ctx, in.InstanceID)
	if err != nil {
		return err
	}
	if !current.Installed || current.PluginID != in.PluginID || current.Version != in.Version || current.Generation != in.Generation ||
		scope.customHosts.init == nil && !originalObjectStillLoaded(scope.object) {
		return bad("PLUGIN_SNAPSHOT_STALE", "自定义Hosts实例已卸载或替换")
	}
	return nil
}

func originalCustomHostsConfigRows(raw json.RawMessage) (mode string, rows []string, err error) {
	var config map[string]json.RawMessage
	if json.Unmarshal(raw, &config) != nil {
		return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts配置不是对象")
	}
	if len(config) == 0 {
		return "", nil, nil
	}
	if !originalBaseJSONTruthy(config["enabled"]) {
		return "clear", []string{}, nil
	}
	hosts := bytes.TrimSpace(config["hosts"])
	rows = []string{}
	if len(hosts) == 0 || hosts[0] != '"' && !originalBaseJSONTruthy(hosts) {
		return "clear", rows, nil
	}
	if len(hosts) > builtin.MaxCustomHostsBytes {
		return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts配置超过128 KiB")
	}
	add := func(line string) {
		if line != "" && line != "\n" {
			rows = append(rows, strings.ReplaceAll(line, "\n", ""))
		}
	}
	switch hosts[0] {
	case '"':
		var text string
		if json.Unmarshal(hosts, &text) != nil {
			return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts配置无效")
		}
		for _, line := range strings.Split(text, "\n") {
			add(line)
		}
	case '[':
		var list []json.RawMessage
		if json.Unmarshal(hosts, &list) != nil {
			return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts配置无效")
		}
		for _, entry := range list {
			if !originalBaseJSONTruthy(entry) {
				continue
			}
			var line string
			if json.Unmarshal(entry, &line) != nil {
				return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts的非空行必须为字符串")
			}
			add(line)
		}
	case '{':
		// Python iterates dictionary keys in insertion order.
		decoder := json.NewDecoder(bytes.NewReader(hosts))
		_, _ = decoder.Token()
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return "", nil, err
			}
			var ignored json.RawMessage
			if err = decoder.Decode(&ignored); err != nil {
				return "", nil, err
			}
			line := key.(string)
			if !seen[line] {
				add(line)
				seen[line] = true
			}
		}
	default:
		return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts配置不可迭代")
	}
	if len(rows) > 4096 {
		return "", nil, bad("INVALID_HOSTS_VALUE", "自定义Hosts行数超过4096")
	}
	return "replace", rows, nil
}

func (a *App) originalCustomHostsApply(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	call := scope.customHosts
	if call == nil || scope.object == nil || scope.object.app != a || !originalCustomHostsInstallation(scope.installation) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "自定义Hosts只能在当前实例初始化或重载时修改容器文件")
	}
	var input struct {
		Mode  string   `json:"mode"`
		Lines []string `json:"lines"`
	}
	if err := originalResourceDecodeBounded(raw, &input, 2*builtin.MaxCustomHostsBytes); err != nil {
		return nil, err
	}

	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if err := a.originalCustomHostsCurrent(ctx, scope); err != nil {
		return nil, err
	}
	if call.used || len(call.config) == 0 || call.init != nil && !call.init.active.Load() {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "没有本次自定义Hosts写入的配置依据")
	}
	current, err := a.originalBaseConfigLocked(ctx, scope.installation.InstanceID)
	if err != nil {
		return nil, err
	}
	stamp := a.pluginOriginalBase.config[scope.installation.InstanceID].observed
	mode, rows, err := originalCustomHostsConfigRows(call.config)
	if err != nil {
		return nil, err
	}
	currentMode, currentRows, err := originalCustomHostsConfigRows(current)
	if err != nil {
		return nil, err
	}
	if call.init == nil && stamp != call.fingerprint || currentMode != mode || !slices.Equal(currentRows, rows) {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "自定义Hosts配置已变化")
	}
	if mode == "" || input.Mode != mode || input.Lines == nil || !slices.Equal(input.Lines, rows) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "自定义Hosts请求与当前已保存配置不一致")
	}
	call.used = true
	call.fingerprint = stamp

	path, err := a.originalCustomHostsPath()
	if err != nil {
		return nil, err
	}
	read, err := openOriginalCustomHosts(path, unix.O_RDONLY)
	if err != nil {
		return nil, bad("HOSTS_READ_FAILED", "无法读取容器hosts文件，请检查挂载及权限")
	}
	before, info, err := readOriginalCustomHosts(read)
	read.Close()
	if err != nil {
		return nil, err
	}

	content, normalized, invalid, write := "", "", []string{}, true
	if mode == "replace" {
		content, normalized, invalid, write, err = builtin.BuildCustomHosts122(string(before), rows)
	} else {
		content, err = builtin.CustomHostsCanonicalBase(string(before))
	}
	if err != nil {
		return nil, bad("HOSTS_FILE_UNSAFE", "容器hosts无法按原版规则解析")
	}
	for _, line := range invalid {
		message := "[HOST] 格式转换错误："
		switch len(legacyanime.Fields(line)) {
		case 0:
			message += "list index out of range"
		case 1:
			message += "Address and Name(s) must be specified."
		}
		a.originalCustomHostsMessage(ctx, "custom_hosts_invalid", message)
	}
	if write {
		err = replaceOriginalCustomHosts(ctx, scope, path, info, before, []byte(content))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			message := "更新系统hosts文件失败："
			if mode == "clear" {
				message = "恢复系统hosts文件失败："
			}
			if apiError, ok := errors.AsType[*APIError](err); ok {
				message += apiError.Message
			} else {
				message += "请检查权限"
			}
			a.originalCustomHostsMessage(ctx, "custom_hosts_write_failed", message)
		} else {
			message := "更新系统hosts文件成功"
			if mode == "clear" {
				message = "系统hosts文件已恢复"
			}
			a.logOriginalPluginContext(ctx, "INFO", "custom_hosts_applied", message)
		}
	}
	if mode == "replace" {
		call.expected = &originalCustomHostsSaved{Enabled: err == nil, Hosts: normalized, ErrHosts: invalid}
	}
	return json.Marshal(struct {
		WriteFailed bool     `json:"write_failed"`
		ErrHosts    []string `json:"err_hosts"`
	}{err != nil, invalid})
}

func (a *App) originalCustomHostsSaveLocked(ctx context.Context, scope originalBaseCallScope, target string, raw json.RawMessage) (json.RawMessage, error) {
	call := scope.customHosts
	if call == nil || call.expected == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "自定义Hosts只能保存本次处理的结果")
	}
	if err := a.originalCustomHostsCurrent(ctx, scope); err != nil {
		return nil, err
	}
	var config struct {
		Enabled  *bool     `json:"enabled"`
		Hosts    *string   `json:"hosts"`
		ErrHosts *[]string `json:"err_hosts"`
	}
	if err := originalResourceDecodeBounded(raw, &config, 2*builtin.MaxCustomHostsBytes); err != nil {
		return nil, err
	}
	if config.Enabled == nil || config.Hosts == nil || config.ErrHosts == nil || *config.Enabled != call.expected.Enabled ||
		*config.Hosts != call.expected.Hosts || !slices.Equal(*config.ErrHosts, call.expected.ErrHosts) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "自定义Hosts配置与实际处理结果不一致")
	}
	stamp, _, err := a.pluginStore.readOriginalBaseConfigRow(ctx, a.Store.DB, target)
	if err != nil {
		return nil, err
	}
	if stamp != call.fingerprint {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "自定义Hosts配置已变化")
	}
	call.expected = nil
	return a.originalBaseUpdateConfigLocked(ctx, target, raw)
}

func (a *App) originalCustomHostsMessage(ctx context.Context, event, message string) {
	a.logOriginalPluginContext(ctx, "ERROR", event, message)
	title := "自定义Hosts"
	a.systemMessages.put("plugin", &title, message, nil, time.Now())
}

func (a *App) originalCustomHostsPath() (string, error) {
	if path := a.pluginCustomHostsTestPath; filepath.IsAbs(path) && filepath.Clean(path) == path {
		return path, nil
	}
	const path = "/etc/hosts"
	if runtime.GOOS == "linux" && originalPluginDockerMode() {
		mounts, err := os.Open("/proc/self/mountinfo")
		if err == nil {
			defer mounts.Close()
			scanner := bufio.NewScanner(mounts)
			for scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				if len(fields) > 5 && fields[4] == path && !strings.HasSuffix(fields[3], path) {
					return path, nil
				}
			}
		}
	}
	return "", bad("HOSTS_SCOPE_DENIED", "自定义Hosts需要独立挂载的容器hosts文件")
}

func openOriginalCustomHosts(path string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "container-hosts"), nil
}

func readOriginalCustomHosts(file *os.File) ([]byte, os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > builtin.MaxCustomHostsBytes {
		return nil, nil, bad("HOSTS_FILE_UNSAFE", "容器hosts必须是不超过128 KiB的普通文件")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return nil, nil, bad("HOSTS_FILE_UNSAFE", "容器hosts不能与其他文件共享硬链接")
	}
	raw, err := io.ReadAll(io.LimitReader(file, builtin.MaxCustomHostsBytes+1))
	if err != nil || len(raw) > builtin.MaxCustomHostsBytes || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, nil, bad("HOSTS_READ_FAILED", "无法读取有效的容器hosts文件")
	}
	return raw, info, nil
}

func replaceOriginalCustomHosts(ctx context.Context, scope originalBaseCallScope, path string, snapshot os.FileInfo, before, content []byte) error {
	if len(content) > builtin.MaxCustomHostsBytes || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return bad("INVALID_HOSTS_VALUE", "容器hosts输出无效或超过128 KiB")
	}
	file, err := openOriginalCustomHosts(path, unix.O_RDWR)
	if err != nil {
		return bad("HOSTS_WRITE_FAILED", "请检查挂载及权限")
	}
	defer file.Close()
	fd := int(file.Fd())
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return bad("HOSTS_BUSY", "容器hosts正在被其他操作修改")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	actual, info, err := readOriginalCustomHosts(file)
	if err != nil {
		return err
	}
	if !os.SameFile(info, snapshot) || !bytes.Equal(actual, before) {
		return bad("HOSTS_CHANGED", "容器hosts已变化，未覆盖其他修改")
	}
	if err := scope.validate(ctx); err != nil {
		return err
	}
	if current, err := os.Lstat(path); err != nil || !os.SameFile(info, current) {
		return bad("HOSTS_CHANGED", "容器hosts已变化，未覆盖其他修改")
	}
	if err := writeOriginalCustomHosts(file, content); err != nil {
		if restoreErr := writeOriginalCustomHosts(file, before); restoreErr != nil {
			return bad("HOSTS_RECOVERY_REQUIRED", "写入及恢复均失败，请核对容器hosts")
		}
		return bad("HOSTS_WRITE_FAILED", "写入失败，已恢复原文件")
	}
	if current, err := os.Lstat(path); err != nil || !os.SameFile(info, current) {
		return bad("HOSTS_CHANGED", "容器hosts已变化，请核对实际文件")
	}
	return nil
}

// A Docker file mount cannot be atomically replaced. Update the verified file
// descriptor and use its previous bytes to restore a failed partial write.
func writeOriginalCustomHosts(file *os.File, content []byte) error {
	n, err := file.WriteAt(content, 0)
	if err != nil || n != len(content) {
		return io.ErrShortWrite
	}
	if err := file.Truncate(int64(len(content))); err != nil {
		return err
	}
	return file.Sync()
}
