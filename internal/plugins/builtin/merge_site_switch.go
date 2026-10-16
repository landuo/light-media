package builtin

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"

	"light-media/internal/plugins"
)

// Field names and plugin identities are the original 1.2 bindings. In
// particular BrushFlowLanduo is not an alias for BrushFlow.
type SiteControlSlot struct {
	Key, Follow, PluginID, PeerField string
	Custom                           bool
}

var SiteControlSlots = []SiteControlSlot{
	{Key: "search_sites", Follow: "search_follow_enable_sites"},
	{Key: "rss_sites", Follow: "rss_follow_enable_sites"},
	{Key: "signin_sites", Follow: "signin_follow_enable_sites", PluginID: "AutoSignIn", PeerField: "sign_sites", Custom: true},
	{Key: "login_sites", Follow: "login_follow_enable_sites", PluginID: "AutoSignIn", PeerField: "login_sites", Custom: true},
	{Key: "statistic_sites", Follow: "statistic_follow_enable_sites", PluginID: "SiteStatistic", PeerField: "statistic_sites", Custom: true},
	{Key: "iyuu_seed_sites", Follow: "iyuu_seed_follow_enable_sites", PluginID: "IYUUAutoSeed", PeerField: "sites", Custom: true},
	{Key: "brush_flow_sites", Follow: "brush_flow_follow_enable_sites", PluginID: "BrushFlow", PeerField: "brushsites"},
	{Key: "cross_seed_sites", Follow: "cross_seed_follow_enable_sites", PluginID: "CrossSeed", PeerField: "sites", Custom: true},
}

type SiteControls map[string]json.RawMessage

func (c SiteControls) Sites(key string) []string {
	out := []string{}
	_ = json.Unmarshal(c[key], &out)
	if out == nil {
		return []string{}
	}
	return out
}
func (c SiteControls) Follows(key string) bool {
	var out bool
	_ = json.Unmarshal(c[key], &out)
	return out
}
func (c SiteControls) SetSites(key string, values []string) {
	if values == nil {
		values = []string{}
	}
	c[key], _ = json.Marshal(values)
}
func (c SiteControls) Clone() SiteControls {
	out := SiteControls{}
	for key, raw := range c {
		out[key] = slices.Clone(raw)
	}
	return out
}

func (c SiteControls) Validate() error {
	keys := map[string]bool{"enable_sites": true}
	for _, slot := range SiteControlSlots {
		keys[slot.Key] = true
		keys[slot.Follow] = false
	}
	for key, raw := range c {
		list, ok := keys[key]
		if !ok {
			return errors.New("unknown site control field")
		}
		if list {
			var ids []string
			if json.Unmarshal(raw, &ids) != nil || len(ids) > 1024 {
				return errors.New("invalid site references")
			}
			for _, id := range ids {
				if len(id) == 0 || len(id) > 256 {
					return errors.New("invalid site reference")
				}
				for _, r := range id {
					if r < 32 || r == 127 {
						return errors.New("invalid site reference")
					}
				}
			}
		} else {
			var flag bool
			if json.Unmarshal(raw, &flag) != nil {
				return errors.New("invalid follow flag")
			}
		}
	}
	return nil
}

func (c SiteControls) AnyFollow(installed []string) bool {
	for _, slot := range SiteControlSlots {
		// Original get_state omits CrossSeed, and both original event handlers
		// use that same gate. Preserve it rather than silently fixing the source.
		if slot.PluginID == "CrossSeed" {
			continue
		}
		if c.Follows(slot.Follow) && (slot.PluginID == "" || slices.Contains(installed, slot.PluginID)) {
			return true
		}
	}
	return false
}

func NormalizeSiteControls(c SiteControls, customIDs []string) SiteControls {
	out := c.Clone()
	enabled := c.Sites("enable_sites")
	for _, slot := range SiteControlSlots {
		if !c.Follows(slot.Follow) {
			continue
		}
		values := slices.Clone(enabled)
		if slot.Custom {
			for _, id := range c.Sites(slot.Key) {
				if id != "" && slices.Contains(customIDs, id) {
					values = append(values, id)
				}
			}
		}
		out.SetSites(slot.Key, values)
	}
	return out
}

// Site events append once or remove the first occurrence, as Python list does.
func ChangeSiteControl(values []string, id string, enabled bool) ([]string, bool) {
	out := slices.Clone(values)
	index := slices.Index(out, id)
	if enabled && index < 0 {
		return append(out, id), true
	}
	if !enabled && index >= 0 {
		return append(out[:index], out[index+1:]...), true
	}
	return out, false
}

type SiteControlOption struct {
	Title string `json:"title"`
	Value string `json:"value"`
}
type SiteControlSnapshot struct {
	Config      SiteControls        `json:"config"`
	Sites       []SiteControlOption `json:"sites"`
	CustomSites []SiteControlOption `json:"custom_sites"`
	Installed   []string            `json:"installed"`
	Revision    int64               `json:"revision"`
}

//go:embed merge_site_switch_form.json
var mergeSiteSwitchForm []byte

func MergeSiteSwitchManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "MergeSiteSwitch", Name: "聚合站点开关", Description: "统一管理所有与站点相关的开关。", Icon: "world.png", Version: "1.2", Author: "hotlcc", AuthorURL: "https://github.com/hotlcc", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{"sites.controls.read", "sites.controls.configure"}}
}

func HandleMergeSiteSwitch(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		value, err := host(ctx, "sites.controls.read", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		var snapshot SiteControlSnapshot
		if json.Unmarshal(value, &snapshot) != nil || snapshot.Config.Validate() != nil {
			return nil, errors.New("invalid site controls snapshot")
		}
		var form []map[string]any
		if json.Unmarshal(mergeSiteSwitchForm, &form) != nil {
			return nil, errors.New("invalid original site controls form")
		}
		rows := []any{}
		for _, row := range form[0]["content"].([]any) {
			include := true
			var visit func(any)
			visit = func(node any) {
				switch n := node.(type) {
				case []any:
					for _, child := range n {
						visit(child)
					}
				case map[string]any:
					if n["component"] == "VSelect" {
						props := n["props"].(map[string]any)
						model, _ := props["model"].(string)
						options := snapshot.Sites
						for _, slot := range SiteControlSlots {
							if slot.Key == model {
								if slot.PluginID != "" && !slices.Contains(snapshot.Installed, slot.PluginID) {
									include = false
								}
								if slot.Custom {
									options = append(slices.Clone(options), snapshot.CustomSites...)
								}
							}
						}
						props["items"] = options
					}
					if children, ok := n["content"]; ok {
						visit(children)
					}
				}
			}
			visit(row)
			if include {
				rows = append(rows, row)
			}
		}
		form[0]["content"] = rows
		return json.Marshal(map[string]any{"conf": form, "model": snapshot.Config, "revision": snapshot.Revision})
	}
	var invocation plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &invocation) != nil {
		return nil, errors.New("invalid site switch invocation")
	}
	if invocation.Action != "init" && invocation.Action != "reload" {
		return nil, errors.New("unsupported site switch action")
	}
	var activation struct {
		ConfigSaved bool `json:"config_saved"`
	}
	if len(invocation.Input) > 0 && json.Unmarshal(invocation.Input, &activation) != nil {
		return nil, errors.New("invalid site switch activation")
	}
	return host(ctx, "sites.controls.configure", json.RawMessage(`{}`))
}
