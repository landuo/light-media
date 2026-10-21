// Package statistic ports SiteStatistic 1.9.7's snapshot selection, precision
// warnings, views and notification calculation. No collection or I/O occurs here.
package statistic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const PrecisionNote = "≈ 表示两期读数疑似经过 PB 舍入，显示步长约 1.02T；两期差值的误差可达一个步长，读数未变不代表没有流量，无法据此还原真实单日增量。"
const pbStep = float64(1<<50) / 1000

type Data struct {
	SiteID      string   `json:"site_id,omitempty"`
	Domain      string   `json:"domain"`
	Name        string   `json:"name"`
	Username    *string  `json:"username"`
	UserLevel   *string  `json:"user_level"`
	Upload      *float64 `json:"upload"`
	Download    *float64 `json:"download"`
	Ratio       *float64 `json:"ratio"`
	Bonus       *float64 `json:"bonus"`
	Seeding     *float64 `json:"seeding"`
	SeedingSize *float64 `json:"seeding_size"`
	UpdatedDay  string   `json:"updated_day"`
	UpdatedTime string   `json:"updated_time,omitempty"`
	Error       string   `json:"err_msg"`
}

type Snapshot struct {
	Latest        []Data    `json:"latest"`
	History       []Data    `json:"history"`
	ActiveDomains []string  `json:"active_domains"`
	Now           time.Time `json:"now"`
}

type Selected struct {
	Day               string
	Current, Previous []Data
}
type Node struct {
	Component string         `json:"component"`
	Props     map[string]any `json:"props,omitempty"`
	Text      *any           `json:"text,omitempty"`
	Content   []Node         `json:"content,omitempty"`
}

func n(component string, props map[string]any, children ...Node) Node {
	return Node{Component: component, Props: props, Content: children}
}
func text(component string, props map[string]any, value any) Node {
	// A nil value is deliberately retained as JSON null, matching the source's
	// table metadata, rather than fabricating a zero observation.
	return Node{Component: component, Props: props, Text: &value}
}
func number(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func Select(snapshot Snapshot) (Selected, error) {
	out := Selected{Current: []Data{}, Previous: []Data{}}
	if len(snapshot.Latest) > 2048 || len(snapshot.History) > 32768 {
		return out, errors.New("site snapshot exceeds limit")
	}
	active := map[string]bool{}
	for _, domain := range snapshot.ActiveDomains {
		active[domain] = true
	}
	for _, data := range append(slices.Clone(snapshot.Latest), snapshot.History...) {
		if _, err := time.Parse("2006-01-02", data.UpdatedDay); err != nil {
			return out, errors.New("invalid site snapshot date")
		}
		for _, v := range []*float64{data.Upload, data.Download, data.Ratio, data.Bonus, data.Seeding, data.SeedingSize} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
				return out, errors.New("invalid site snapshot number")
			}
		}
	}
	for _, data := range snapshot.Latest {
		if active[data.Domain] {
			out.Current = append(out.Current, data)
		}
	}
	if len(out.Current) == 0 {
		return out, nil
	}
	out.Day = out.Current[0].UpdatedDay
	for _, data := range out.Current {
		if data.UpdatedDay != out.Day {
			out.Day = "各站点最近更新日"
			break
		}
	}
	sort.SliceStable(out.Current, func(i, j int) bool { return number(out.Current[i].Upload) > number(out.Current[j].Upload) })
	// The original dictionaries match by name, including the last row on a
	// same-date duplicate. Domain filtering applies to latest rows only.
	byDate := map[string]map[string]Data{}
	for _, data := range snapshot.History {
		if byDate[data.UpdatedDay] == nil {
			byDate[data.UpdatedDay] = map[string]Data{}
		}
		byDate[data.UpdatedDay][data.Name] = data
	}
	for _, data := range out.Current {
		day, _ := time.Parse("2006-01-02", data.UpdatedDay)
		previous, exists := byDate[day.AddDate(0, 0, -1).Format("2006-01-02")][data.Name]
		if !exists || previous.Error != "" {
			for back := 2; back <= 7; back++ {
				candidate, ok := byDate[day.AddDate(0, 0, -back).Format("2006-01-02")][data.Name]
				if ok && candidate.Error == "" {
					previous, exists = candidate, true
					break
				}
			}
		}
		if exists {
			out.Previous = append(out.Previous, previous)
		}
	}
	return out, nil
}

func Approximate(current, previous []Data) map[string]map[string]bool {
	prior := map[string]Data{}
	for _, data := range previous {
		prior[data.Name] = data
	}
	result := map[string]map[string]bool{}
	for _, data := range current {
		p, ok := prior[data.Name]
		if !ok || p.UpdatedDay == data.UpdatedDay || p.Error != "" || data.Error != "" {
			continue
		}
		fields := map[string]bool{}
		for _, field := range []struct {
			name          string
			before, after *float64
		}{{"upload", p.Upload, data.Upload}, {"download", p.Download, data.Download}} {
			before, after := number(field.before), number(field.after)
			if after < before {
				continue
			}
			quantized := true
			for _, v := range []float64{before, after} {
				ulp := math.Nextafter(v, math.Inf(1)) - v
				if v < float64(1000)*(1<<40) || math.Abs(v-math.RoundToEven(v/pbStep)*pbStep) > math.Max(1, 2*ulp) {
					quantized = false
				}
			}
			if quantized {
				fields[field.name] = true
			}
		}
		if len(fields) > 0 {
			result[data.Name] = fields
		}
	}
	return result
}

func pyFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
func rounded(v float64, precision int) float64 {
	// Decimal formatting rounds the represented IEEE value, as Python round
	// does; scaling by 10 first would change half-way cases.
	s := strconv.FormatFloat(v, 'f', precision, 64)
	r, _ := strconv.ParseFloat(s, 64)
	return r
}
func FormatSize(value *float64) string {
	if value == nil {
		return ""
	}
	v := *value
	if v >= 1<<50 {
		return fmt.Sprintf("%.2fPB", v/(1<<50))
	}
	v = math.Trunc(v)
	if v < 0 {
		return strconv.FormatFloat(v, 'f', 0, 64) + "B"
	}
	// Source bisect_left subtracts one from inclusive (1024^n - 1) bounds.
	bounds := []float64{1023, 1048575, 1073741823, 1099511627775}
	index := sort.SearchFloat64s(bounds, v) - 1
	if index < 0 {
		return pyFloat(v) + "B"
	}
	return pyFloat(rounded(v/(bounds[index]+1), 2)) + string("KMGT"[index])
}
func Traffic(v float64, approximate, increment bool) string {
	if approximate && increment && v == 0 {
		return "≈ 读数未变（实际增量未知）"
	}
	prefix := ""
	if approximate {
		prefix = "≈ "
	}
	return prefix + FormatSize(&v)
}
func comma(s string) string {
	parts := strings.SplitN(s, ".", 2)
	whole := parts[0]
	start := 0
	if strings.HasPrefix(whole, "-") {
		start = 1
	}
	for i := len(whole) - 3; i > start; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if len(parts) == 2 {
		whole += "." + parts[1]
	}
	return whole
}

func card(label, icon string, value string) Node {
	return n("VCol", map[string]any{"cols": 6, "md": 3}, n("VCard", map[string]any{"variant": "tonal"},
		n("VCardText", map[string]any{"class": "d-flex align-center"},
			n("VAvatar", map[string]any{"rounded": true, "variant": "text", "class": "me-3"}, n("VImg", map[string]any{"src": "/plugin_icon/" + icon + ".png"})),
			n("div", nil, text("span", map[string]any{"class": "text-caption"}, label), n("div", map[string]any{"class": "d-flex align-center flex-wrap"}, text("span", map[string]any{"class": "text-h6"}, value))))))
}

func Elements(selected Selected, mode string) []Node {
	approx := Approximate(selected.Current, selected.Previous)
	anyApprox := map[string]bool{}
	for _, fields := range approx {
		for field := range fields {
			anyApprox[field] = true
		}
	}
	result := []Node{}
	if len(approx) > 0 {
		byName := map[string]Data{}
		for _, data := range selected.Previous {
			byName[data.Name] = data
		}
		details := []Node{text("div", nil, PrecisionNote)}
		for _, data := range selected.Current {
			for _, field := range []struct {
				name, label  string
				value, prior *float64
			}{{"upload", "上传", data.Upload, byName[data.Name].Upload}, {"download", "下载", data.Download, byName[data.Name].Download}} {
				if approx[data.Name][field.name] {
					details = append(details, text("div", nil, fmt.Sprintf("%s %s（%s → %s）：%s", data.Name, field.label, byName[data.Name].UpdatedDay, data.UpdatedDay, Traffic(math.Trunc(number(field.value))-math.Trunc(number(field.prior)), true, true))))
				}
			}
		}
		result = append(result, n("VCol", map[string]any{"cols": 12}, n("VAlert", map[string]any{"type": "info", "variant": "tonal", "class": "text-break"}, details...)))
	}
	if mode == "total" || mode == "all" {
		var upload, download, seed, seedSize float64
		hasSeed := false
		for _, data := range selected.Current {
			upload += number(data.Upload)
			download += number(data.Download)
			seed += number(data.Seeding)
			hasSeed = hasSeed || number(data.Seeding) != 0
			seedSize += number(data.SeedingSize)
		}
		seedText := "0"
		if hasSeed {
			seedText = comma(pyFloat(seed))
		}
		result = append(result, card("总上传量", "upload", Traffic(upload, anyApprox["upload"], false)), card("总下载量", "download", Traffic(download, anyApprox["download"], false)), card("总做种数", "seed", seedText), card("总做种体积", "database", FormatSize(&seedSize)))
	}
	if mode == "today" || mode == "all" {
		// Source charts preserve first occurrence order but overwrite a duplicate
		// name's values, and use the first matching previous record here.
		order := []string{}
		increments := map[string][2]float64{}
		for _, data := range selected.Current {
			for _, p := range selected.Previous {
				if p.Name != data.Name {
					continue
				}
				values := [2]float64{}
				for i, pair := range [][2]*float64{{data.Upload, p.Upload}, {data.Download, p.Download}} {
					if pair[0] != nil && pair[1] != nil && math.Trunc(*pair[1]) > 0 {
						values[i] = math.Trunc(*pair[0]) - math.Trunc(*pair[1])
					}
				}
				if _, ok := increments[data.Name]; !ok {
					order = append(order, data.Name)
				}
				increments[data.Name] = values
				break
			}
		}
		for index, field := range []struct{ name, label string }{{"upload", "上传"}, {"download", "下载"}} {
			labels := []string{}
			values := []float64{}
			total := 0.0
			for _, name := range order {
				value := increments[name][index]
				if value <= 0 {
					continue
				}
				prefix := ""
				if approx[name][field.name] {
					prefix = "≈ "
				}
				labels = append(labels, prefix+name)
				gb := rounded(value/(1<<30), 1)
				values = append(values, gb)
				total += gb
			}
			prefix := ""
			if anyApprox[field.name] {
				prefix = "≈ "
			}
			options := map[string]any{"chart": map[string]any{"type": "pie"}, "labels": labels, "title": map[string]any{"text": fmt.Sprintf("%s增量（%s）共 %s%s GB", field.label, selected.Day, prefix, pyFloat(rounded(total, 2)))}, "legend": map[string]any{"show": true}, "plotOptions": map[string]any{"pie": map[string]any{"expandOnClick": false}}, "noData": map[string]any{"text": "暂无数据"}}
			// Python sum([]) is int(0), whose rounded display is "0".
			if len(values) == 0 {
				options["title"] = map[string]any{"text": fmt.Sprintf("%s增量（%s）共 %s0 GB", field.label, selected.Day, prefix)}
			}
			result = append(result, n("VCol", map[string]any{"cols": 12, "md": 6}, n("VApexChart", map[string]any{"height": 300, "options": options, "series": values})))
		}
	}
	return result
}

func Page(selected Selected) []Node {
	if len(selected.Current) == 0 {
		return []Node{text("div", map[string]any{"class": "text-center"}, "暂无数据")}
	}
	headers := []Node{}
	for _, label := range []string{"站点", "数据日期", "用户名", "用户等级", "上传量", "下载量", "分享率", "魔力值", "做种数", "做种体积"} {
		headers = append(headers, text("th", map[string]any{"class": "text-start ps-4"}, label))
	}
	rows := []Node{}
	approx := Approximate(selected.Current, selected.Previous)
	for _, data := range selected.Current {
		values := []any{data.Name, data.UpdatedDay, data.Username, data.UserLevel, Traffic(number(data.Upload), approx[data.Name]["upload"], false), Traffic(number(data.Download), approx[data.Name]["download"], false), data.Ratio, comma(fmt.Sprintf("%.1f", number(data.Bonus))), data.Seeding, FormatSize(data.SeedingSize)}
		if data.Upload == nil {
			values[4] = ""
		}
		if data.Download == nil {
			values[5] = ""
		}
		classes := []string{"whitespace-nowrap break-keep text-high-emphasis", "", "", "", "text-success", "text-error", "", "", "", ""}
		cells := []Node{}
		for i, value := range values {
			var props map[string]any
			if classes[i] != "" {
				props = map[string]any{"class": classes[i]}
			}
			cells = append(cells, text("td", props, value))
		}
		rows = append(rows, n("tr", map[string]any{"class": "text-sm"}, cells...))
	}
	elements := Elements(selected, "all")
	elements = append(elements, n("VCol", map[string]any{"cols": 12}, n("VTable", map[string]any{"hover": true}, n("thead", nil, headers...), n("tbody", nil, rows...))))
	return []Node{n("VRow", nil, elements...)}
}

type Notification struct{ Title, Text, Fingerprint string }

func BuildNotification(selected Selected, mode string, today string) *Notification {
	if mode == "" {
		return nil
	}
	current, previous := map[string]Data{}, map[string]Data{}
	order := []string{}
	for _, data := range selected.Current {
		if _, ok := current[data.Name]; !ok {
			order = append(order, data.Name)
		}
		current[data.Name] = data
	}
	for _, data := range selected.Previous {
		previous[data.Name] = data
	}
	approx := Approximate(selected.Current, selected.Previous)
	anyApprox := map[string]bool{}
	type message struct {
		key  float64
		text string
	}
	messages := []message{}
	uploadTotal, downloadTotal := 0.0, 0.0
	for index, name := range order {
		data := current[name]
		upload, download := math.Trunc(number(data.Upload)), math.Trunc(number(data.Download))
		if mode == "inc" {
			p, ok := previous[name]
			if !ok {
				continue
			}
			pu, pd := math.Trunc(number(p.Upload)), math.Trunc(number(p.Download))
			if pu == 0 && pd == 0 {
				continue
			}
			// 1.9.7's notification calculation differs from its charts: only both
			// zero baselines skip the site. Preserve this observable source behavior.
			upload, download = math.Max(0, upload-pu), math.Max(0, download-pd)
		}
		if upload <= 0 && download <= 0 && len(approx[name]) == 0 {
			continue
		}
		updated := ""
		if data.UpdatedDay != "" && data.UpdatedDay != today {
			updated = "（" + data.UpdatedDay + "）"
		}
		uploadTotal += upload
		downloadTotal += download
		for field := range approx[name] {
			anyApprox[field] = true
		}
		body := fmt.Sprintf("【%s】%s\n上传量：%s\n下载量：%s\n————————————", name, updated, Traffic(upload, approx[name]["upload"], mode == "inc"), Traffic(download, approx[name]["download"], mode == "inc"))
		key := upload + float64(index)/1000
		// A float key collision overwrites the original dict entry.
		replaced := false
		for i := range messages {
			if messages[i].key == key {
				messages[i].text = body
				replaced = true
				break
			}
		}
		if !replaced {
			messages = append(messages, message{key, body})
		}
	}
	if len(messages) == 0 {
		return nil
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].key > messages[j].key })
	parts := []string{fmt.Sprintf("【汇总】\n总上传：%s\n总下载：%s\n————————————", Traffic(uploadTotal, anyApprox["upload"], mode == "inc"), Traffic(downloadTotal, anyApprox["download"], mode == "inc"))}
	for _, message := range messages {
		parts = append(parts, message.text)
	}
	if len(anyApprox) > 0 {
		parts = append(parts, PrecisionNote)
	}
	body := strings.Join(parts, "\n")
	digest := sha256.Sum256([]byte(body))
	return &Notification{Title: "站点数据统计", Text: body, Fingerprint: hex.EncodeToString(digest[:])}
}
