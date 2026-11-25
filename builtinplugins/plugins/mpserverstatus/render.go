package mpserverstatus

import "fmt"

func node(component string, props Object, content ...any) Object {
	o := Object{"component": component}
	if props != nil {
		o["props"] = props
	}
	if content != nil {
		o["content"] = content
	}
	return o
}
func text(component string, props Object, value any) Object {
	o := node(component, props)
	o["text"] = value
	return o
}
func card(label, value, icon, color, subtitle string, cols, sm, md int) Object {
	props := Object{"cols": cols, "md": md}
	if sm != 0 {
		props["sm"] = sm
	}
	if subtitle == "" {
		subtitle = "-"
	}
	return node("VCol", props, node("VCard", Object{"variant": "tonal", "class": "h-100"}, node("VCardText", Object{"class": "d-flex align-center ga-3"}, node("div", Object{"class": "flex-grow-1", "style": "min-width: 0;"}, text("span", Object{"class": "text-caption text-medium-emphasis text-truncate d-block"}, label), text("div", Object{"class": "text-h6 text-truncate"}, value), text("div", Object{"class": "text-caption text-medium-emphasis text-truncate"}, subtitle)), text("VIcon", Object{"color": color, "size": "28", "class": "flex-shrink-0"}, icon))))
}
func stat(label, value, icon, color, subtitle string) Object {
	return card(label, value, icon, color, subtitle, 6, 0, 3)
}
func table(title string, rows [][2]string, md int) Object {
	body := []any{}
	for _, row := range rows {
		body = append(body, node("tr", nil, text("td", Object{"class": "text-medium-emphasis text-no-wrap"}, row[0]), text("td", Object{"class": "text-end"}, row[1])))
	}
	return node("VCol", Object{"cols": 12, "md": md}, node("VCard", Object{"variant": "tonal"}, text("VCardTitle", Object{"class": "text-subtitle-1"}, title), node("VTable", Object{"hover": true, "density": "compact"}, node("tbody", nil, body...))))
}
func summary(p Object, message, typ string) Object {
	return text("VAlert", Object{"type": typ, "variant": "tonal", "density": "compact", "class": "mb-3"}, fmt.Sprintf("%s · HTTP %v · %v", message, dash(p["status_code"]), dash(p["host"])))
}
func value(o Object, k string) string { return fmt.Sprint(dash(o[k])) }
func python(v any) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprint(v)
}
func dashboardCards(m, p Object) []any {
	a, waiting, busy, rate, accepts := "-", "-", "-", "-", "-"
	activeSub, waitingSub, busySub, sample := "暂无连接数据", "暂无连接数据", "暂无连接数据", "暂无采样"
	if len(m) > 0 {
		a = FormatInteger(m["active_connections"])
		waiting = FormatInteger(m["waiting"])
		busy = FormatInteger(m["busy"])
		rate = FormatRate(m["requests_rate"], "次/秒")
		accepts = FormatRate(m["accepts_rate"], "个/秒")
		activeSub = "忙碌 " + FormatPercent(m["busy_percent"])
		waitingSub = FormatPercent(m["waiting_percent"]) + " 空闲"
		busySub = fmt.Sprintf("读 %v / 写 %v", get(m, "reading"), get(m, "writing"))
		sample = FormatSample(m["sample_seconds"])
	}
	return []any{card("响应延迟", FormatSeconds(p["seconds"]), "mdi-speedometer", Latency(p["seconds"], truth(p["ok"])), "HTTP "+value(p, "status_code"), 12, 6, 4), card("活跃连接", a, "mdi-lan-connect", "primary", activeSub, 12, 6, 4), card("等待连接", waiting, "mdi-timer-sand", "warning", waitingSub, 12, 6, 4), card("处理中连接", busy, "mdi-swap-horizontal", "warning", busySub, 12, 6, 4), card("请求速率", rate, "mdi-chart-timeline-variant", "primary", sample, 12, 6, 4), card("连接速度", accepts, "mdi-connection", "info", sample, 12, 6, 4)}
}
func Elements(s Snapshot, dashboard bool) []any {
	m, p, d, t := s.Metrics, s.Probe, s.DNS, s.TLS
	message, typ := "服务状态正常", "success"
	unavailable := s.Error != "" || len(m) == 0
	if unavailable {
		message = s.Error
		if message == "" {
			message = "无法连接服务器"
		}
		typ = "error"
	}
	if dashboard {
		if unavailable {
			m = nil
		}
		return []any{summary(p, message, typ), node("VRow", nil, dashboardCards(m, p)...)}
	}
	cards := []any{}
	if unavailable {
		errOrProtocol := value(p, "error")
		if !truth(p["error"]) {
			errOrProtocol = value(p, "protocol")
		}
		ips := stringList(d["ips"])
		cards = append(cards, stat("HTTP状态", value(p, "status_code"), "mdi-web-cancel", "error", errOrProtocol), stat("响应延迟", FormatSeconds(p["seconds"]), "mdi-speedometer-slow", "warning", "本次探测"), stat("DNS解析", fmt.Sprint(len(ips)), "mdi-dns-outline", Severity(float64(len(ips)), 1, 0), FormatIPs(d["ips"], d["error"])), stat("TLS证书", FormatTLSDays(t), "mdi-shield-lock-outline", TLSColor(t), value(t, "expires_at")), table("服务探测", [][2]string{{"地址", value(p, "url")}, {"域名", python(d["host"]) + ":" + python(d["port"])}, {"解析IP", FormatIPs(d["ips"], d["error"])}, {"DNS耗时", FormatSeconds(d["seconds"])}, {"TLS版本", value(t, "version")}, {"TLS错误", value(t, "error")}, {"Server", value(p, "server")}, {"Content-Type", value(p, "content_type")}}, 12))
	} else {
		cards = append(cards, stat("HTTP状态", value(p, "status_code"), "mdi-web-check", "success", value(p, "protocol")), stat("响应延迟", FormatSeconds(p["seconds"]), "mdi-speedometer", "info", FormatSize(get(p, "content_bytes"))+" 响应"), stat("活跃连接", FormatInteger(m["active_connections"]), "mdi-lan-connect", "primary", "忙碌 "+FormatPercent(m["busy_percent"])), stat("请求速率", FormatRate(m["requests_rate"], "次/秒"), "mdi-chart-timeline-variant", "primary", FormatSample(m["sample_seconds"])), stat("连接速率", FormatRate(m["accepts_rate"], "个/秒"), "mdi-connection", "info", FormatSample(m["sample_seconds"])), stat("等待连接", FormatInteger(m["waiting"]), "mdi-timer-sand", "warning", FormatPercent(m["waiting_percent"])+" 空闲"), stat("处理中连接", FormatInteger(m["busy"]), "mdi-swap-horizontal", "warning", fmt.Sprintf("读 %v / 写 %v", get(m, "reading"), get(m, "writing"))), stat("请求/连接", FormatFloat(m["requests_per_connection"], 2), "mdi-counter", "secondary", "累计均值"), stat("总请求数", FormatInteger(m["requests"]), "mdi-format-list-numbered", "primary", "Nginx 累计"), stat("总连接数", FormatInteger(m["accepts"]), "mdi-server-network", "primary", "Nginx 累计"), stat("丢弃连接", FormatInteger(m["dropped"]), "mdi-alert-circle-outline", Severity(number(get(m, "dropped")), 1, 10), FormatPercent(m["dropped_percent"])), stat("TLS证书", FormatTLSDays(t), "mdi-shield-lock-outline", TLSColor(t), value(t, "expires_at")))
		cards = append(cards, table("连接明细", [][2]string{{"Reading", FormatInteger(m["reading"]) + " / " + FormatPercent(m["reading_percent"])}, {"Writing", FormatInteger(m["writing"]) + " / " + FormatPercent(m["writing_percent"])}, {"Waiting", FormatInteger(m["waiting"]) + " / " + FormatPercent(m["waiting_percent"])}, {"Handled", FormatInteger(m["handled"])}, {"Dropped", FormatInteger(m["dropped"]) + " / " + FormatPercent(m["dropped_percent"])}}, 6), table("服务探测", [][2]string{{"域名", python(d["host"]) + ":" + python(d["port"])}, {"解析IP", FormatIPs(d["ips"], d["error"])}, {"DNS耗时", FormatSeconds(d["seconds"])}, {"TLS版本", value(t, "version")}, {"证书签发", value(t, "issuer")}, {"服务时间", value(p, "date")}, {"Server", value(p, "server")}, {"Content-Type", value(p, "content_type")}}, 6))
	}
	return []any{summary(p, message, typ), node("VRow", nil, cards...)}
}
