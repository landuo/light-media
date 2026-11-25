package sitestatistic

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const PrecisionNote = "≈ 表示两期读数疑似经过 PB 舍入，显示步长约 1.02T；两期差值的误差可达一个步长，读数未变不代表没有流量，无法据此还原真实单日增量。"
const pbStep = float64(1<<50) / 1000

func n(component string, props map[string]any, children ...Node) Node {
	v := Node{"component": component}
	if props != nil {
		v["props"] = props
	}
	if len(children) > 0 {
		v["content"] = children
	}
	return v
}
func text(component string, props map[string]any, value any) Node {
	v := n(component, props)
	v["text"] = value
	return v
}
func Approximate(current, previous []Data) map[string]map[string]bool {
	prior := map[string]Data{}
	for _, d := range previous {
		prior[dataKey(d)] = d
	}
	result := map[string]map[string]bool{}
	for _, d := range current {
		p := prior[dataKey(d)]
		if p == nil || p["updated_day"] == d["updated_day"] || truth(p["err_msg"]) || truth(d["err_msg"]) {
			continue
		}
		fields := map[string]bool{}
		for _, field := range []string{"upload", "download"} {
			before, after := number(p[field]), number(d[field])
			if after < before {
				continue
			}
			valid := true
			for _, v := range []float64{before, after} {
				if math.IsNaN(v) || math.IsInf(v, 0) || v < float64(1000)*(1<<40) || math.Abs(v-math.RoundToEven(v/pbStep)*pbStep) > math.Max(1, 2*(math.Nextafter(v, math.Inf(1))-v)) {
					valid = false
					break
				}
			}
			if valid {
				fields[field] = true
			}
		}
		if len(fields) > 0 {
			result[dataKey(d)] = fields
		}
	}
	return result
}
func sourceFileSize(value any) string {
	if value == nil {
		return ""
	}
	s := pyText(value)
	s = sourceByteSuffix.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if sourcePythonSpace(r) {
			return -1
		}
		return r
	}, s)
	numericText := strings.ReplaceAll(s, ".", "")
	digits := sourceIsDigit(numericText)
	if digits {
		v, e := sourceFloat(s)
		if e != nil {
			return ""
		}
		bounds := []float64{1023, 1048575, 1073741823, 1099511627775}
		i := sort.SearchFloat64s(bounds, v) - 1
		if i < 0 {
			return pyFloat(v) + "B"
		}
		return pyFloat(rounded(v/(bounds[i]+1), 2)) + string("KMGT"[i])
	}
	if strings.ContainsAny(s, "KkMmGgTtPp") {
		return s
	}
	return s + "B"
}

var sourceByteSuffix = regexp.MustCompile(`(?i)i?b`)

func FormatSize(value any) string {
	if value == nil {
		return ""
	}
	v, e := numeric(value)
	if e != nil {
		var source *SourceError
		if errors.As(e, &source) && source.Kind == "OverflowError" {
			panic(e)
		}
		return sourceFileSize(value)
	}
	if v >= 1<<50 {
		if math.IsInf(v, 1) {
			return "infPB"
		}
		return strconv.FormatFloat(v/(1<<50), 'f', 2, 64) + "PB"
	}
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return sourceFileSize(value)
	}
	return sourceFileSize(integer(v))
}
func Traffic(value any, approximate, increment bool) string {
	if approximate && increment && !truth(value) {
		return "≈ 读数未变（实际增量未知）"
	}
	prefix := ""
	if approximate {
		prefix = "≈ "
	}
	return prefix + FormatSize(value)
}
func adjacentDays(previous, current any) bool {
	before, e := sourceDate(previous)
	if e != nil {
		return false
	}
	after, e := sourceDate(current)
	if e != nil {
		return false
	}
	return sourceDay(before.AddDate(0, 0, 1)) == sourceDay(after)
}

func comma(s string) string {
	if strings.ContainsAny(s, "eE") {
		return s
	}
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
func card(label, icon, value string) Node {
	return n("VCol", map[string]any{"cols": 6, "md": 3}, n("VCard", map[string]any{"variant": "tonal"}, n("VCardText", map[string]any{"class": "d-flex align-center"}, n("VAvatar", map[string]any{"rounded": true, "variant": "text", "class": "me-3"}, n("VImg", map[string]any{"src": "/plugin_icon/" + icon + ".png"})), n("div", nil, text("span", map[string]any{"class": "text-caption"}, label), n("div", map[string]any{"class": "d-flex align-center flex-wrap"}, text("span", map[string]any{"class": "text-h6"}, value))))))
}
func Elements(selected Selected, mode any) (nodes []Node, err error) {
	defer recoverError(&err)
	return elements(selected, mode), nil
}
func elements(selected Selected, mode any) []Node {
	approx := Approximate(selected.Current, selected.Previous)
	anyApprox := map[string]bool{}
	for _, fields := range approx {
		for field := range fields {
			anyApprox[field] = true
		}
	}
	result := []Node{}
	if len(approx) > 0 {
		prior := map[string]Data{}
		for _, d := range selected.Previous {
			prior[dataKey(d)] = d
		}
		details := []Node{text("div", nil, PrecisionNote)}
		for _, d := range selected.Current {
			p := prior[dataKey(d)]
			for i, field := range []string{"upload", "download"} {
				if approx[dataKey(d)][field] {
					difference := integer(d[field])
					difference.Sub(difference, integer(p[field]))
					label := []string{"上传", "下载"}[i]
					details = append(details, text("div", nil, pyText(d["name"])+" "+label+"（"+pyText(p["updated_day"])+" → "+pyText(d["updated_day"])+"）："+Traffic(difference, true, true)))
				}
			}
		}
		result = append(result, n("VCol", map[string]any{"cols": 12}, n("VAlert", map[string]any{"type": "info", "variant": "tonal", "class": "text-break"}, details...)))
	}
	modeText, _ := mode.(string)
	if modeText == "total" || modeText == "all" {
		totals := map[string]float64{}
		seedCount := 0
		for _, field := range []string{"upload", "download", "seeding", "seeding_size"} {
			values := []float64{}
			for _, d := range selected.Current {
				if truth(d[field]) {
					values = append(values, number(d[field]))
				}
			}
			totals[field] = floatSum(values)
			if field == "seeding" {
				seedCount = len(values)
			}
		}
		seedText := "0"
		if seedCount > 0 {
			seedText = comma(pyFloat(totals["seeding"]))
		}
		result = append(result, card("总上传量", "upload", Traffic(totals["upload"], anyApprox["upload"], false)), card("总下载量", "download", Traffic(totals["download"], anyApprox["download"], false)), card("总做种数", "seed", seedText), card("总做种体积", "database", FormatSize(totals["seeding_size"])))
	}
	if modeText == "today" || modeText == "all" {
		order := []string{}
		increments := map[string][2]float64{}
		names := map[string]any{}
		for _, d := range selected.Current {
			for _, p := range selected.Previous {
				if dataKey(p) != dataKey(d) {
					continue
				}
				// A daily increment needs the immediately preceding calendar day.
				// The source selection may fall back up to seven days after failed
				// collections; that difference must not be shown as one day.
				if !adjacentDays(p["updated_day"], d["updated_day"]) {
					break
				}
				inc := subtraction(d, p)
				if len(inc) == 0 {
					break
				}
				values := [2]float64{}
				for i, field := range []string{"upload", "download"} {
					if value := inc[field]; value != nil {
						values[i], _ = newFloat(value)
					}
				}
				key := dataKey(d)
				if _, known := increments[key]; !known {
					order = append(order, key)
				}
				increments[key] = values
				names[key] = d["name"]
				break
			}
		}
		if len(order) == 0 {
			result = append(result, n("VCol", map[string]any{"cols": 12}, n("VAlert", map[string]any{"type": "info", "variant": "tonal"}, text("div", nil, "没有相邻两天的有效数据，无法计算 "+pyText(selected.Day)+" 的日增量"))))
		} else {
			for index, field := range []string{"upload", "download"} {
				labels := []string{}
				values := []float64{}
				for _, key := range order {
					value := increments[key][index]
					if value <= 0 {
						continue
					}
					name, ok := names[key].(string)
					if !ok {
						panic("can only concatenate str to source site name")
					}
					prefix := ""
					if approx[key][field] {
						prefix = "≈ "
					}
					labels = append(labels, prefix+name)
					values = append(values, rounded(value/(1<<30), 1))
				}
				total := "0"
				if len(values) > 0 {
					total = pyFloat(rounded(floatSum(values), 2))
				}
				prefix := ""
				if anyApprox[field] {
					prefix = "≈ "
				}
				label := []string{"上传", "下载"}[index]
				options := map[string]any{"chart": map[string]any{"type": "pie"}, "labels": labels, "title": map[string]any{"text": label + "增量（" + pyText(selected.Day) + "）共 " + prefix + total + " GB"}, "legend": map[string]any{"show": true}, "plotOptions": map[string]any{"pie": map[string]any{"expandOnClick": false}}, "noData": map[string]any{"text": "暂无数据"}}
				result = append(result, n("VCol", map[string]any{"cols": 12, "md": 6}, n("VApexChart", map[string]any{"height": 300, "options": options, "series": values})))
			}
		}
	}
	return result
}
func Page(selected Selected) (nodes []Node, err error) {
	defer recoverError(&err)
	if len(selected.Current) == 0 {
		return []Node{text("div", map[string]any{"class": "text-center"}, "暂无数据")}, nil
	}
	contents := elements(selected, "all")
	headers := []Node{}
	for _, label := range []string{"站点", "数据日期", "用户名", "用户等级", "上传量", "下载量", "分享率", "魔力值", "做种数", "做种体积"} {
		headers = append(headers, text("th", map[string]any{"class": "text-start ps-4"}, label))
	}
	rows := []Node{}
	approx := Approximate(selected.Current, selected.Previous)
	for _, d := range selected.Current {
		bonus, e := numeric(d["bonus"])
		if !truth(d["bonus"]) {
			bonus, e = 0, nil
		}
		bonusText := "0.0"
		if e == nil {
			if math.IsNaN(bonus) || math.IsInf(bonus, 0) {
				bonusText = pyFloat(bonus)
			} else {
				bonusText = comma(strconv.FormatFloat(bonus, 'f', 1, 64))
			}
		} else {
			var source *SourceError
			if errors.As(e, &source) && source.Kind != "ValueError" {
				panic(e)
			}
		}
		values := []any{d["name"], d["updated_day"], d["username"], d["user_level"], Traffic(d["upload"], approx[dataKey(d)]["upload"], false), Traffic(d["download"], approx[dataKey(d)]["download"], false), d["ratio"], bonusText, d["seeding"], FormatSize(d["seeding_size"])}
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
	contents = append(contents, n("VCol", map[string]any{"cols": 12}, n("VTable", map[string]any{"hover": true}, n("thead", nil, headers...), n("tbody", nil, rows...))))
	return []Node{n("VRow", nil, contents...)}, nil
}
