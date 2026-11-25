package mergemessagenotify

import (
	"context"
	"fmt"
)

func (p *Plugin) log(ctx context.Context, level, text string) {
	if p.host.Log != nil {
		p.host.Log(ctx, level, text)
	}
}

// DispatchEvent mirrors the original catch-and-continue event entry point.
// Report is diagnostic only; the original externally visible return is None.
func (p *Plugin) DispatchEvent(ctx context.Context, event map[string]any) Dispatch {
	return p.dispatch(ctx, event, nil)
}
func (p *Plugin) dispatch(ctx context.Context, event map[string]any, send func(context.Context, string, Message) (any, error)) Dispatch {
	report := Dispatch{Attempts: []Attempt{}}
	config := p.Config()
	if !truth(config["enable"]) || len(event) == 0 {
		report.Ignored = true
		return report
	}
	channels := config["enable_channels"]
	p.log(ctx, "INFO", "监听到发送消息通知事件")
	const abort = "发送消息通知事件监听任务执行中止: "
	var reason string
	switch {
	case !truth(channels):
		reason = "没有启用任何渠道"
	case truth(event["channel"]):
		reason = "忽略系统渠道的消息"
	case !truth(event["title"]) && !truth(event["text"]):
		reason = "标题和内容不允许同时为空"
	}
	if reason != "" {
		p.log(ctx, "WARNING", abort+reason)
		report.Ignored = true
		return report
	}
	values, err := iterable(channels)
	if err != nil {
		report.Error = err.Error()
		p.log(ctx, "ERROR", "发送消息通知事件监听任务执行异常: "+report.Error)
		return report
	}
	message := Message{Title: event["title"], Text: event["text"], Ext: cloneMap(event)}
	t := or(event["type"], event["mtype"])
	switch v := t.(type) {
	case *NotificationType:
		message.Type = v
	case NotificationType:
		message.Type = &v
	case map[string]any:
		if name, ok := v["$enum"].(string); ok {
			message.Type = NotifyType(name)
		}
	case string:
		// Ordinary host notifications serialize the source enum's display
		// value. Keep the original fields unchanged for channel templates.
		for _, kind := range NotificationTypes {
			if kind.Value == v {
				message.Type = &kind
				break
			}
		}
		if message.Type == nil {
			message.RawType = v
		}
	case nil:
	default:
		message.RawType = v
	}
	known := map[string]Channel{}
	for _, c := range p.Channels() {
		known[c.Key] = c
	}
	for _, value := range values {
		if config["channel_strategy"] == "ORDER_SUCCESS_ONE" && report.Success > 0 {
			break
		}
		if !truth(value) {
			continue
		}
		key, ok := value.(string)
		if !ok {
			switch value.(type) {
			case []any, map[string]any:
				report.Error = "TypeError"
				p.log(ctx, "ERROR", "发送消息通知事件监听任务执行异常: "+report.Error)
				return report
			default:
				continue
			}
		}
		registered, exists := known[key]
		if !exists {
			continue
		}
		var result any
		var err error
		if send != nil {
			result, err = send(ctx, key, message)
		} else {
			result, err = p.Send(ctx, key, message)
		}
		a := Attempt{Key: key, Result: result}
		if err != nil {
			a.Error = err.Error()
			report.Failed++
			p.log(ctx, "ERROR", "消息发送执行异常: 渠道 = "+registered.Name)
		} else {
			if truth(result) {
				report.Success++
			} else {
				report.Failed++
			}
			p.log(ctx, "INFO", "消息发送执行完成: 渠道 = "+registered.Name+" success = "+str(result))
		}
		report.Attempts = append(report.Attempts, a)
	}
	p.log(ctx, "INFO", fmt.Sprintf("发送消息通知事件监听任务执行成功: 成功渠道数 = %d, 失败渠道数 = %d", report.Success, report.Failed))
	return report
}
