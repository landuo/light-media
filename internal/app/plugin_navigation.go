package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/sourcejson"
	"math/big"
	"slices"
	"sort"
	"strings"
)

// Fixed manager get_plugin_sidebar_nav snapshots loaded objects in insertion
// order; state/render exceptions are outside the per-plugin getter boundary.
func (a *App) pluginNavigation(ctx context.Context) (any, error) {
	items := []map[string]any{}
	for _, object := range a.originalLoadedPluginSnapshot() {
		enabled, err := object.getState(ctx)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		if !slices.Contains(object.manifest.Methods, "get_sidebar_nav") || !slices.Contains(object.manifest.Methods, "get_render_mode") {
			continue
		}
		mode, _, _, err := object.originalViewRenderMode(ctx)
		if err != nil {
			return nil, err
		}
		if mode != "vue" {
			continue
		}
		raw, _, err := object.originalViewRead(ctx, "get_sidebar_nav", nil)
		if err == nil {
			name, nameErr := object.originalDisplayName(ctx)
			err = nameErr
			if err == nil {
				var entries []map[string]any
				entries, err = originalSidebarEntries(object.installation.InstanceID, name, raw)
				items = append(items, entries...)
			}
		}
		if err != nil {
			a.log("ERROR", "plugins", "plugin_sidebar_failed", "插件侧栏方法读取失败", "", "")
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		for _, key := range []string{"section", "order", "plugin_id", "nav_key"} {
			if key == "order" {
				x, _ := new(big.Int).SetString(string(items[i][key].(json.Number)), 10)
				y, _ := new(big.Int).SetString(string(items[j][key].(json.Number)), 10)
				if comparison := x.Cmp(y); comparison != 0 {
					return comparison < 0
				}
			} else {
				x, y := items[i][key].(string), items[j][key].(string)
				if x != y {
					return x < y
				}
			}
		}
		return false
	})
	return items, nil
}

func originalSidebarEntries(id, name string, raw json.RawMessage) ([]map[string]any, error) {
	result := []map[string]any{}
	if !originalBaseJSONTruthy(raw) {
		return result, nil
	}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		// Python iterates dictionary keys/string characters and skips them.
		switch bytes.TrimSpace(raw)[0] {
		case '{', '"':
			return result, nil
		}
		return result, errors.New("sidebar declaration is not iterable")
	}
	for _, row := range rows {
		var entry map[string]json.RawMessage
		if json.Unmarshal(row, &entry) != nil || len(entry) == 0 {
			continue
		}
		key := entry["nav_key"]
		if !originalBaseJSONTruthy(key) {
			key = entry["key"]
		}
		if !originalBaseJSONTruthy(key) {
			key = json.RawMessage(`"main"`)
		}
		text, err := sourcejson.String(key)
		if err != nil {
			return result, err
		}
		nav := legacyanime.Strip(text)
		if nav == "" || strings.ContainsAny(nav, "/?# ") {
			continue
		}
		title := entry["title"]
		if !originalBaseJSONTruthy(title) {
			title, _ = json.Marshal(name)
		}
		icon := entry["icon"]
		if !originalBaseJSONTruthy(icon) {
			icon = json.RawMessage(`"mdi-puzzle"`)
		}
		section := "system"
		if originalBaseJSONTruthy(entry["section"]) {
			text, err := sourcejson.String(entry["section"])
			if err != nil {
				return result, err
			}
			section = legacyanime.LowerCase(text)
		}
		if !slices.Contains([]string{"start", "discovery", "subscribe", "organize", "system"}, section) {
			section = "system"
		}
		var permission any
		if value := bytes.TrimSpace(entry["permission"]); len(value) > 0 && !bytes.Equal(value, []byte("null")) {
			text, err := sourcejson.String(value)
			if err != nil {
				return result, err
			}
			if slices.Contains([]string{"subscribe", "discovery", "search", "manage", "admin"}, text) {
				permission = text
			}
		}
		order, err := originalSidebarOrder(entry["order"])
		if err != nil {
			return result, err
		}
		result = append(result, map[string]any{"plugin_id": id, "instance_id": id, "nav_key": nav, "title": title, "icon": icon, "section": section, "permission": permission, "order": order})
	}
	return result, nil
}

func originalSidebarOrder(raw json.RawMessage) (json.Number, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "0", nil
	}
	if text, ok := value.(string); ok {
		number, err := legacyanime.Integer(text)
		if err != nil {
			return "0", nil
		}
		return json.Number(number.String()), nil
	}
	if number, infinity, ok := originalBaseNumber(value); ok {
		// int(inf) raises OverflowError outside the source's inner TypeError /
		// ValueError catch, aborting this plugin after any previous valid items.
		if infinity != 0 {
			return "", errors.New("sidebar order integer overflow")
		}
		integer := new(big.Int).Quo(number.Num(), number.Denom())
		return json.Number(integer.String()), nil
	}
	return "0", nil
}
