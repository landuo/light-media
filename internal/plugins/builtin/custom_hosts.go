package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/pythonidna"
	"light-media/internal/releasemeta/legacyanime"
)

const CustomHostsMarker = "# CustomHostsPlugin"
const MaxCustomHostsBytes = 128 << 10

type CustomHostsConfiguration struct {
	emptySource bool
	Enabled     bool            `json:"enabled"`
	Hosts       json.RawMessage `json:"hosts"`
	ErrHosts    json.RawMessage `json:"err_hosts"`
}

func (c CustomHostsConfiguration) Lines() ([]string, error) {
	return customHostsLines(c.Hosts)
}

func (c *CustomHostsConfiguration) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return errors.New("invalid_custom_hosts_config")
	}
	c.emptySource = len(fields) == 0
	c.Enabled = systemProcessTruth(fields["enabled"])
	c.Hosts = fields["hosts"]
	c.ErrHosts = fields["err_hosts"]
	return nil
}
func (c CustomHostsConfiguration) Validate() error {
	if !c.Enabled {
		return nil
	}
	_, e := c.Lines()
	return e
}

func customHostsLines(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > MaxCustomHostsBytes {
		return nil, errors.New("hosts value too large")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		lines := strings.Split(text, "\n")
		if len(lines) > 4096 {
			return nil, errors.New("hosts line limit")
		}
		return lines, nil
	}
	if !systemProcessTruth(raw) {
		return nil, nil
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("hosts must be text or lines")
	}
	if len(values) > 4096 {
		return nil, errors.New("hosts line limit")
	}
	lines := []string{}
	for _, value := range values {
		if !systemProcessTruth(value) {
			continue
		}
		var line string
		if json.Unmarshal(value, &line) != nil {
			return nil, errors.New("source_hosts_replace_type_error")
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func CustomHostsManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "CustomHosts", Name: "自定义Hosts", Description: "修改系统hosts文件，加速网络访问。", Icon: "hosts.png", Version: "1.2.1", Author: "thsrite", AuthorURL: "https://github.com/thsrite", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{"hosts.read", "hosts.replace", "config.patch", "notification.post"}, Events: []string{"plugin.reload"}}
}

const customHostsForm = `[{"component":"VForm","content":[
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"md":6},"content":[{"component":"VSwitch","props":{"model":"enabled","label":"启用插件"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VTextarea","props":{"model":"hosts","label":"自定义hosts","rows":10,"placeholder":"每行一个配置，格式为：ip host1 host2 ..."}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VTextarea","props":{"model":"err_hosts","readonly":true,"label":"错误hosts","rows":2,"placeholder":"错误的hosts配置会展示在此处，请修改上方hosts重新提交（错误的hosts不会写入系统hosts文件）"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VAlert","props":{"type":"info","variant":"tonal","text":"host格式ip host，中间有空格！！！（注：容器运行则更新容器hosts！非宿主机！）"}}]}]}
]}]`

type customHostEntry struct {
	address string
	names   []string
	comment string
}

func customHostsName(value string) string {
	value = legacyanime.LowerCase(legacyanime.Strip(value))
	if normalized, e := pythonidna.ToASCII(value); e == nil {
		return normalized
	}
	return value
}

func customHostsEntry(line string) (customHostEntry, bool) {
	fields := legacyanime.Fields(line)
	if len(fields) < 2 {
		return customHostEntry{}, false
	}
	address, err := netip.ParseAddr(fields[0])
	if err != nil || address.Zone() != "" {
		return customHostEntry{}, false
	}
	entry := customHostEntry{address: fields[0]}
	for _, name := range fields[1:] {
		entry.names = append(entry.names, customHostsName(name))
	}
	return entry, true
}

func CustomHostsBase(text string) string {
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		if legacyanime.Strip(line) == CustomHostsMarker {
			return text[:offset]
		}
		offset += len(line)
	}
	return text
}

// CustomHostsCanonicalBase reproduces python_hosts parsing/serialization up to
// the managed marker. The full source file is parsed first, including its tail.
func CustomHostsCanonicalBase(original string) (string, error) {
	original = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(original)
	entries := []customHostEntry{}
	for _, line := range strings.SplitAfter(original, "\n") {
		if line == "" {
			continue
		}
		trimmed := legacyanime.Strip(line)
		if trimmed == "" {
			entries = append(entries, customHostEntry{})
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			entries = append(entries, customHostEntry{comment: trimmed})
			continue
		}
		all := legacyanime.Fields(trimmed)
		if len(all) == 0 {
			continue
		}
		address, e := netip.ParseAddr(all[0])
		if e != nil || address.Zone() != "" {
			continue
		}
		head, comment, hasComment := strings.Cut(line, "#")
		fields := legacyanime.Fields(head)
		if len(fields) < 2 {
			return "", errors.New("source_invalid_hosts_entry")
		}
		entry := customHostEntry{address: fields[0]}
		for _, name := range fields[1:] {
			entry.names = append(entry.names, customHostsName(name))
		}
		if hasComment {
			entry.comment = legacyanime.Strip(comment)
		}
		entries = append(entries, entry)
	}
	var out strings.Builder
	for _, entry := range entries {
		if entry.address == "" && entry.comment == CustomHostsMarker {
			break
		}
		if entry.address != "" {
			out.WriteString(entry.address + "\t" + strings.Join(entry.names, " "))
			if entry.comment != "" {
				out.WriteString(" # " + entry.comment)
			}
		} else {
			out.WriteString(entry.comment)
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}
func BuildCustomHosts(original string, lines []string) (content, normalized string, invalid []string, hasValid bool) {
	content, normalized, invalid, hasValid, _ = buildCustomHostsSource(original, lines)
	return
}
func buildCustomHostsSource(original string, lines []string) (content, normalized string, invalid []string, hasValid bool, err error) {
	filtered := make([]string, 0, len(lines))
	for _, raw := range lines {
		if raw != "" && raw != "\n" {
			filtered = append(filtered, strings.ReplaceAll(raw, "\n", ""))
		}
	}
	return buildCustomHostsRows(original, filtered, false)
}

// BuildCustomHosts122 applies the installed source's allow_address_duplication
// option. Each row has already passed the source's initial empty/LF filter.
func BuildCustomHosts122(original string, lines []string) (content, normalized string, invalid []string, hasValid bool, err error) {
	return buildCustomHostsRows(original, lines, true)
}

func buildCustomHostsRows(original string, lines []string, allowAddressDuplication bool) (content, normalized string, invalid []string, hasValid bool, err error) {
	base, err := CustomHostsCanonicalBase(original)
	if err != nil {
		return original, "", nil, false, err
	}
	addresses, names := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(base, "\n") {
		head, _, _ := strings.Cut(line, "#")
		if entry, ok := customHostsEntry(head); ok {
			addresses[entry.address] = true
			for _, name := range entry.names {
				names[name] = true
			}
		}
	}
	var added []customHostEntry
	var config strings.Builder
	invalid = []string{}
	for _, raw := range lines {
		config.WriteString(raw + "\n")
		line := legacyanime.Strip(raw)
		if strings.HasPrefix(line, "#") {
			added = append(added, customHostEntry{comment: line})
			hasValid = true
			continue
		}
		entry, ok := customHostsEntry(line)
		if !ok {
			invalid = append(invalid, line+"\n")
			continue
		}
		hasValid = true
		duplicate := !allowAddressDuplication && entry.address != "0.0.0.0" && entry.address != "127.0.0.1" && addresses[entry.address]
		for _, name := range entry.names {
			duplicate = duplicate || names[name]
		}
		if duplicate {
			continue
		}
		added = append(added, entry)
		addresses[entry.address] = true
		for _, name := range entry.names {
			names[name] = true
		}
	}
	normalized = config.String()
	if !hasValid {
		return original, normalized, invalid, false, nil
	}
	var out strings.Builder
	out.WriteString(base)
	out.WriteString(CustomHostsMarker + "\n")
	for _, entry := range added {
		if entry.comment != "" {
			out.WriteString(entry.comment)
		} else {
			out.WriteString(entry.address + "\t" + strings.Join(entry.names, " "))
		}
		out.WriteByte('\n')
	}
	return out.String(), normalized, invalid, true, nil
}

func HandleCustomHosts(ctx context.Context, method string, payload json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		return json.Marshal(map[string]any{"conf": json.RawMessage(customHostsForm), "model": map[string]any{"enabled": false, "hosts": "", "err_hosts": ""}})
	}
	var input plugins.Invocation
	var config CustomHostsConfiguration
	if method != "invoke" || json.Unmarshal(payload, &input) != nil || (input.Action != "init" && input.Action != "reload") || json.Unmarshal(input.Config, &config) != nil {
		return nil, errors.New("invalid custom hosts invocation")
	}
	if config.emptySource {
		return json.Marshal(map[string]any{"enabled": false, "applied": false, "invalid_count": 0})
	}
	var lines []string
	var err error
	if config.Enabled {
		lines, err = config.Lines()
		if err != nil {
			return nil, err
		}
	}
	raw, err := host(ctx, "hosts.read", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var snapshot struct {
		Content string `json:"content"`
		Digest  string `json:"digest"`
	}
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil, errors.New("invalid hosts snapshot")
	}
	content, err := CustomHostsCanonicalBase(snapshot.Content)
	if err != nil {
		return nil, err
	}
	normalized, invalid := "", []string{}
	apply := config.Enabled && len(lines) > 0
	write := !apply
	if apply {
		var valid bool
		content, normalized, invalid, valid, err = buildCustomHostsSource(snapshot.Content, lines)
		if err != nil {
			return nil, err
		}
		write = valid
	}
	if len(invalid) > 0 {
		messages := []string{}
		for _, line := range invalid {
			reason := ""
			n := len(legacyanime.Fields(line))
			if n == 0 {
				reason = "list index out of range"
			} else if n < 2 {
				reason = "Address and Name(s) must be specified."
			}
			messages = append(messages, "[HOST] 格式转换错误："+reason)
		}
		notice, _ := json.Marshal(map[string]any{"title": "自定义Hosts", "messages": messages})
		if _, err = host(ctx, "notification.post", notice); err != nil {
			return nil, err
		}
	}
	var writeErr error
	if write {
		request, _ := json.Marshal(map[string]string{"content": content, "expected_digest": snapshot.Digest})
		_, writeErr = host(ctx, "hosts.replace", request)
	}
	if writeErr != nil {
		message := "恢复系统hosts文件失败：请检查权限"
		if apply {
			message = "更新系统hosts文件失败：请检查权限"
		}
		notice, _ := json.Marshal(map[string]string{"title": "自定义Hosts", "message": message})
		if _, err = host(ctx, "notification.post", notice); err != nil {
			return nil, err
		}
	}
	if apply {
		patch, _ := json.Marshal(map[string]any{"enabled": config.Enabled && writeErr == nil, "hosts": normalized, "err_hosts": invalid})
		if _, err = host(ctx, "config.patch", patch); err != nil {
			return nil, err
		}
	}
	if writeErr != nil {
		return nil, writeErr
	}
	return json.Marshal(map[string]any{"enabled": config.Enabled, "applied": write, "invalid_count": len(invalid)})
}
