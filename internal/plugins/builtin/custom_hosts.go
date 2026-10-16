package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"light-media/internal/plugins"
)

const CustomHostsMarker = "# CustomHostsPlugin"
const MaxCustomHostsBytes = 128 << 10

type CustomHostsConfiguration struct {
	Enabled  bool            `json:"enabled"`
	Hosts    json.RawMessage `json:"hosts"`
	ErrHosts json.RawMessage `json:"err_hosts"`
}

func (c CustomHostsConfiguration) Lines() ([]string, error) {
	return customHostsLines(c.Hosts)
}

func (c CustomHostsConfiguration) Validate() error {
	if _, err := c.Lines(); err != nil {
		return err
	}
	_, err := customHostsLines(c.ErrHosts)
	return err
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
	var lines []string
	if json.Unmarshal(raw, &lines) != nil {
		return nil, errors.New("hosts must be text or lines")
	}
	if len(lines) > 4096 {
		return nil, errors.New("hosts line limit")
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

var customHostsIDNA = idna.New(idna.MapForLookup(), idna.Transitional(true), idna.StrictDomainName(false), idna.ValidateLabels(false), idna.VerifyDNSLength(false))

func customHostsName(value string) string {
	value = cases.Lower(language.Und).String(strings.TrimSpace(value))
	if normalized, err := customHostsIDNA.ToASCII(value); err == nil {
		return normalized
	}
	return value
}

func customHostsEntry(line string) (customHostEntry, bool) {
	fields := strings.Fields(line)
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
		if strings.TrimSpace(line) == CustomHostsMarker {
			return text[:offset]
		}
		offset += len(line)
	}
	return text
}

// BuildCustomHosts follows the installed python_hosts defaults too: duplicate
// addresses/names are skipped, except 0.0.0.0/127.0.0.1 allow address reuse.
// Invalid rows stay in the config and are returned in err_hosts, not written.
func BuildCustomHosts(original string, lines []string) (content, normalized string, invalid []string, hasValid bool) {
	base := CustomHostsBase(original)
	addresses, names := map[string]bool{}, map[string]bool{}
	for _, row := range strings.Split(base, "\n") {
		row, _, _ = strings.Cut(row, "#")
		if entry, valid := customHostsEntry(row); valid {
			addresses[entry.address] = true
			for _, name := range entry.names {
				names[name] = true
			}
		}
	}
	var added []customHostEntry
	var normalizedText strings.Builder
	invalid = []string{}
	for _, line := range lines {
		if line == "" || line == "\n" {
			continue
		}
		line = strings.ReplaceAll(line, "\n", "")
		normalizedText.WriteString(line + "\n")
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			added = append(added, customHostEntry{comment: line})
			hasValid = true
			continue
		}
		entry, valid := customHostsEntry(line)
		if !valid {
			invalid = append(invalid, line+"\n")
			continue
		}
		hasValid = true
		duplicate := false
		if entry.address != "0.0.0.0" && entry.address != "127.0.0.1" {
			duplicate = addresses[entry.address]
		}
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
	normalized = normalizedText.String()
	if !hasValid {
		return original, normalized, invalid, false
	}
	if base != "" && !strings.HasSuffix(base, "\n") {
		base += "\n"
	}
	var out strings.Builder
	out.WriteString(base)
	out.WriteString(CustomHostsMarker + "\n")
	for _, entry := range added {
		if entry.comment != "" {
			out.WriteString(entry.comment + "\n")
		} else {
			out.WriteString(entry.address + "\t" + strings.Join(entry.names, " ") + "\n")
		}
	}
	return out.String(), normalized, invalid, true
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
	lines, err := config.Lines()
	if err != nil {
		return nil, err
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
	content := CustomHostsBase(snapshot.Content)
	normalized, invalid := "", []string{}
	apply := config.Enabled && len(lines) > 0
	if apply {
		content, normalized, invalid, _ = BuildCustomHosts(snapshot.Content, lines)
	}
	var writeErr error
	if content != snapshot.Content {
		request, _ := json.Marshal(map[string]string{"content": content, "expected_digest": snapshot.Digest})
		_, writeErr = host(ctx, "hosts.replace", request)
	}
	if apply {
		patch, _ := json.Marshal(map[string]any{"enabled": config.Enabled && writeErr == nil, "hosts": normalized, "err_hosts": invalid})
		if _, err := host(ctx, "config.patch", patch); err != nil {
			return nil, err
		}
		if len(invalid) > 0 {
			messages := make([]string, 0, len(invalid))
			for _, row := range invalid {
				reason := ""
				if len(strings.Fields(row)) == 0 {
					reason = "list index out of range"
				} else if len(strings.Fields(row)) < 2 {
					reason = "Address and Name(s) must be specified."
				}
				messages = append(messages, "[HOST] 格式转换错误："+reason)
			}
			notice, _ := json.Marshal(map[string]any{"title": "自定义Hosts", "messages": messages})
			if _, err := host(ctx, "notification.post", notice); err != nil {
				return nil, err
			}
		}
	}
	if writeErr != nil {
		notice, _ := json.Marshal(map[string]string{"title": "自定义Hosts", "message": "写入系统hosts文件失败，请检查容器挂载及文件权限。"})
		_, _ = host(ctx, "notification.post", notice)
		return nil, writeErr
	}
	return json.Marshal(map[string]any{"enabled": config.Enabled, "applied": content != snapshot.Content, "invalid_count": len(invalid)})
}
