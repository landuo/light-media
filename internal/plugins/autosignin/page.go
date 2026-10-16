package autosignin

import (
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"time"
)

//go:embed page.css
var pageCSS string
var ErrHistoryShapeNotReady = errors.New("history shape outside verified compatibility")

type siteInfoEntry struct {
	Key  any
	Name string
}

func siteInfoKey(key any) string {
	if value, ok := numeric(key); ok {
		return "number:" + value.RatString()
	}
	return typeName(key) + ":" + pythonScalar(key)
}
func (e *Engine) siteInfo() map[string]siteInfoEntry {
	info := map[string]siteInfoEntry{}
	put := func(key any, name string) {
		hash := siteInfoKey(key)
		entry, exists := info[hash]
		if !exists {
			entry.Key = key
		}
		entry.Name = name
		info[hash] = entry
	}
	add := func(site Object) {
		id, name := site["id"], site["name"]
		if id == nil || !Truth(name) {
			return
		}
		label, _ := name.(string)
		put(id, label)
		put(pythonScalar(id), label)
	}
	for _, site := range e.Inventory.Indexers {
		if !Truth(site["public"]) {
			add(site)
		}
	}
	for _, site := range e.Inventory.Configured {
		add(site)
	}
	for _, site := range e.custom() {
		add(site)
	}
	return info
}
func displayName(id any, info map[string]siteInfoEntry) string {
	label := pythonScalar(id)
	entry, exists := info[siteInfoKey(label)]
	if !exists || entry.Name == "" {
		entry = info[siteInfoKey(id)]
	}
	if entry.Name == "" {
		return ""
	}
	same := map[string]bool{}
	for _, candidate := range info {
		if candidate.Name == entry.Name {
			same[pythonScalar(candidate.Key)] = true
		}
	}
	if len(same) > 1 {
		return entry.Name + "（" + label + "）"
	}
	return entry.Name
}
func sourceStatus(record Object) (string, error) {
	value, exists := record["status"]
	if !exists {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", ErrHistoryShapeNotReady
	}
	return text, nil
}
func (e *Engine) resolvedIDs(selected any) ([]any, error) {
	all, err := contains(selected, "all")
	if err != nil {
		return nil, err
	}
	if !all {
		return asList(selected), nil
	}
	ids := []any{}
	for _, site := range append(append([]Object{}, e.Inventory.Configured...), e.custom()...) {
		ids = append(ids, site["id"])
	}
	return ids, nil
}
func pageNode(component string, props Object, children ...any) Object {
	node := Object{"component": component}
	if props != nil {
		node["props"] = props
	}
	if children != nil {
		node["content"] = children
	}
	return node
}
func pageText(component string, props Object, text any) Object {
	node := pageNode(component, props)
	node["text"] = text
	return node
}
func (e *Engine) Page() ([]any, error) {
	now := e.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := []time.Time{}
	for i := 0; i < 14; i++ {
		days = append(days, today.AddDate(0, 0, -i))
	}
	signin, login := []Object{}, []Object{}
	info := e.siteInfo()
	for _, day := range days {
		label, date := dateLabel(day), day.Format("2006-01-02")
		if records, ok := e.history(label).([]any); ok {
			for _, item := range records {
				record, ok := item.(Object)
				if !ok {
					continue
				}
				record["date"], record["day_obj"] = label, day
				if record["site_id"] != nil {
					if name := displayName(record["site_id"], info); name != "" {
						record["site"] = name
					}
				}
				status, err := sourceStatus(record)
				if err != nil {
					return nil, err
				}
				hasLogin, err := contains(status, "登录")
				if err != nil {
					return nil, err
				}
				if !Truth(record["type"]) && !hasLogin && StatusMeta(status)["level"] == "success" {
					status += "（历史结果未确认）"
					record["status"] = status
				}
				if record["type"] == "登录" || !Truth(record["type"]) && hasLogin {
					login = append(login, record)
				} else {
					signin = append(signin, record)
					if Truth(record["logged_in"]) {
						login = append(login, mergeObject(record, Object{"status": "登录成功", "type": "登录", "success": true}))
					}
				}
			}
		}
		for _, operation := range []string{"签到", "登录"} {
			history := asObject(e.history(operation + "-" + date))
			if !Truth(history) {
				continue
			}
			done, retry := asList(history["do"]), history["retry"]
			if retry == nil {
				retry = []any{}
			}
			for _, id := range done {
				name := displayName(id, info)
				if name == "" {
					continue
				}
				result := asObject(asObject(history["results"])[pythonScalar(id)])
				status := ""
				if Truth(result) {
					fallback := "签到结果未确认"
					if operation == "登录" {
						fallback = "登录结果未确认"
					}
					if Truth(result["message"]) {
						var ok bool
						status, ok = result["message"].(string)
						if !ok {
							return nil, ErrHistoryShapeNotReady
						}
					} else {
						status = fallback
					}
					if operation == "签到" {
						if !Truth(result["success"]) && !Truth(result["logged_in"]) && StatusMeta(status)["level"] == "success" {
							status = "签到失败：" + status
						}
						if Truth(result["success"]) {
							has, _ := contains(status, "登录成功")
							if has {
								status += "（未执行签到）"
							}
						}
						if Truth(result["logged_in"]) {
							login = append(login, Object{"site": name, "status": "登录成功", "date": label, "day_obj": day, "site_id": id})
						}
					}
				} else {
					list := signin
					if operation == "登录" {
						list = login
					}
					existing := false
					for _, record := range list {
						if record["site"] == name && record["date"] == label {
							existing = true
							break
						}
					}
					if existing {
						continue
					}
					needsRetry, err := contains(retry, id)
					if err != nil {
						return nil, err
					}
					if operation == "签到" {
						status = "签到结果未确认"
						if needsRetry {
							status = "需要重试"
						}
					} else {
						status = "登录结果未确认"
						if needsRetry {
							status = "登录需要重试"
						}
					}
				}
				record := Object{"site": name, "status": status, "date": label, "day_obj": day, "site_id": id}
				if operation == "签到" {
					signin = append(signin, record)
				} else {
					login = append(login, record)
				}
			}
		}
	}
	if len(signin) == 0 && len(login) == 0 && !Truth(e.SignSites) && !Truth(e.LoginSites) {
		return []any{pageNode("VAlert", Object{"type": "info", "text": "暂无签到数据", "variant": "tonal", "class": "mt-4", "prepend-icon": "mdi-information"})}, nil
	}
	group := func(records []Object) (map[string][]Object, error) {
		groups := map[string][]Object{}
		for _, record := range records {
			name := "未知站点"
			if value, exists := record["site"]; exists {
				var ok bool
				name, ok = value.(string)
				if !ok {
					return nil, ErrHistoryShapeNotReady
				}
			}
			old := groups[name]
			replaced := false
			for i, previous := range old {
				if previous["date"] == record["date"] {
					old[i] = record
					replaced = true
					break
				}
			}
			if !replaced {
				old = append(old, record)
			}
			groups[name] = old
		}
		return groups, nil
	}
	signGroups, err := group(signin)
	if err != nil {
		return nil, err
	}
	loginGroups, err := group(login)
	if err != nil {
		return nil, err
	}
	for _, entry := range []struct {
		selected any
		groups   map[string][]Object
	}{{e.SignSites, signGroups}, {e.LoginSites, loginGroups}} {
		ids, err := e.resolvedIDs(entry.selected)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			name := displayName(id, info)
			if name != "" {
				if _, exists := entry.groups[name]; !exists {
					entry.groups[name] = []Object{}
				}
			}
		}
	}
	label := dateLabel(today)
	return []any{pageText("style", nil, pageCSS), pageNode("div", Object{"class": "autosignin-page"}, pageSummary(pageStats(signGroups, label), pageStats(loginGroups, label), 14), pageSection("签到状态", "mdi-calendar-check", signGroups, days[:7], "暂无签到数据"), pageSection("登录状态", "mdi-login-variant", loginGroups, days[:7], "暂无登录数据"))}, nil
}
func latestRecord(records []Object, label string) Object {
	var latest Object
	min := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, record := range records {
		if label != "" && record["date"] != label {
			continue
		}
		day, ok := record["day_obj"].(time.Time)
		if !ok {
			day = min
		}
		if latest == nil || day.After(min) {
			latest = record
			min = day
		}
	}
	if latest == nil {
		return Object{}
	}
	return latest
}
func pageStats(groups map[string][]Object, label string) map[string]int {
	stats := map[string]int{"total": len(groups), "recorded": 0, "success": 0, "warning": 0, "error": 0, "none": 0}
	for _, records := range groups {
		record := latestRecord(records, label)
		if len(record) == 0 {
			stats["none"]++
			continue
		}
		stats["recorded"]++
		level := StatusMeta(record["status"])["level"].(string)
		if _, exists := stats[level]; exists {
			stats[level]++
		}
	}
	return stats
}
func pageStat(label, value, meta, color, icon string) Object {
	return pageNode("div", Object{"class": "autosignin-stat app-card-shell app-card-colorful", "style": "--app-card-accent-rgb: var(--v-theme-" + color + ");"}, pageNode("div", Object{"class": "autosignin-stat__head"}, pageText("VIcon", Object{"size": "x-small", "color": color}, icon), pageText("span", nil, label)), pageText("div", Object{"class": "autosignin-stat__value text-" + color}, value), pageText("div", Object{"class": "autosignin-stat__meta"}, meta))
}
func pageSummary(sign, login map[string]int, days int) Object {
	problem := sign["error"] + sign["warning"]
	color := "info"
	if sign["total"] > 0 {
		color = "warning"
		if problem == 0 && sign["none"] == 0 {
			color = "success"
		}
	}
	if sign["total"] > 0 && sign["error"] > 0 {
		color = "error"
	}
	problemColor := "warning"
	if sign["error"] > 0 {
		problemColor = "error"
	}
	loginColor := "info"
	if login["total"] > 0 && login["error"] == 0 && login["warning"] == 0 {
		loginColor = "success"
	}
	return pageNode("div", Object{"class": "autosignin-summary"}, pageStat("今日签到", fmt.Sprintf("%d/%d", sign["success"], sign["total"]), fmt.Sprintf("失败 %d · 未确认 %d · 未记录 %d", sign["error"], sign["warning"], sign["none"]), color, "mdi-calendar-check"), pageStat("签到未完成", fmt.Sprint(problem), fmt.Sprintf("失败 %d · 未确认 %d", sign["error"], sign["warning"]), problemColor, "mdi-alert-circle-outline"), pageStat("今日登录", fmt.Sprintf("%d/%d", login["success"], login["total"]), fmt.Sprintf("异常 %d · 未记录 %d", login["error"]+login["warning"], login["none"]), loginColor, "mdi-login-variant"), pageStat("历史范围", fmt.Sprintf("%d天", days), "矩阵显示最近7天", "info", "mdi-history"))
}
func pageSection(title, icon string, groups map[string][]Object, days []time.Time, empty string) Object {
	return pageNode("div", Object{"class": "autosignin-section"}, pageNode("div", Object{"class": "autosignin-section-head"}, pageText("VIcon", Object{"size": "small", "color": "primary"}, icon), pageText("span", Object{"class": "autosignin-section-title"}, title), pageNode("VSpacer", nil), pageText("VChip", Object{"size": "x-small", "variant": "tonal", "color": "primary"}, fmt.Sprintf("%d 个站点", len(groups)))), pageTable(groups, days, empty))
}
func pageTable(groups map[string][]Object, days []time.Time, empty string) Object {
	if len(groups) == 0 {
		return pageNode("VAlert", Object{"type": "info", "text": empty, "variant": "tonal", "density": "compact", "prepend-icon": "mdi-information"})
	}
	headers := []any{pageText("th", Object{"class": "text-start"}, "站点"), pageText("th", Object{"class": "text-start"}, "今日")}
	for _, day := range days {
		headers = append(headers, pageText("th", Object{"class": "text-center"}, fmt.Sprintf("%d/%d", day.Month(), day.Day())))
	}
	names := []string{}
	for name := range groups {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		a, b := groups[names[i]], groups[names[j]]
		today := dateLabel(days[0])
		left, right := StatusMeta(latestRecord(a, today)["status"])["sort"].(int), StatusMeta(latestRecord(b, today)["status"])["sort"].(int)
		if left != right {
			return left < right
		}
		latest := func(records []Object) time.Time {
			day, ok := latestRecord(records, "")["day_obj"].(time.Time)
			if !ok {
				return time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			return day
		}
		ad, bd := latest(a), latest(b)
		if !ad.Equal(bd) {
			return ad.After(bd)
		}
		return names[i] < names[j]
	})
	rows := []any{}
	for _, name := range names {
		rows = append(rows, pageRow(name, groups[name], days))
	}
	return pageNode("div", Object{"class": "autosignin-table-wrap"}, pageNode("VTable", Object{"hover": true, "density": "compact", "class": "autosignin-table"}, pageNode("thead", nil, pageNode("tr", nil, headers...)), pageNode("tbody", nil, rows...)))
}
func pageRow(name string, records []Object, days []time.Time) Object {
	meta := StatusMeta(latestRecord(records, dateLabel(days[0]))["status"])
	count := "暂无记录"
	if len(records) > 0 {
		count = fmt.Sprintf("%d 条记录", len(records))
	}
	cells := []any{pageNode("td", nil, pageText("div", Object{"class": "autosignin-site-name", "title": name}, name), pageText("div", Object{"class": "autosignin-site-meta"}, count)), pageNode("td", Object{"class": "autosignin-status-cell"}, pageText("VChip", Object{"size": "x-small", "variant": "tonal", "color": meta["color"], "prepend-icon": meta["icon"]}, meta["label"]))}
	for _, day := range days {
		label := dateLabel(day)
		m := StatusMeta(latestRecord(records, label)["status"])
		cells = append(cells, pageNode("td", Object{"class": "autosignin-dot-cell"}, pageNode("span", Object{"class": "autosignin-dot autosignin-dot--" + m["level"].(string), "title": label + " " + m["label"].(string)}, pageText("VIcon", Object{"size": "x-small"}, m["icon"]))))
	}
	return pageNode("tr", nil, cells...)
}
