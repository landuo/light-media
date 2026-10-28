package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
	"light-media/internal/plugins"
)

// This queue is separate from retired receipt/replay execution. Like the source
// MessageHelper it retains pending calls in memory and consumes sender failures.
type originalNoticeRuntime struct {
	mu    sync.Mutex
	queue *notice.Queue
}

func (a *App) originalNoticeQueue(ctx context.Context) (*notice.Queue, error) {
	a.pluginOriginalNotice.mu.Lock()
	defer a.pluginOriginalNotice.mu.Unlock()
	periods, err := a.pluginStore.noticePeriods(ctx)
	if err != nil {
		return nil, err
	}
	if a.pluginOriginalNotice.queue == nil {
		a.pluginOriginalNotice.queue, err = notice.NewQueue(periods, time.Now, func() bool {
			a.pluginOriginalBackground.mu.Lock()
			defer a.pluginOriginalBackground.mu.Unlock()
			return a.pluginOriginalBackground.closed
		}, a.sendOriginalNoticeModules)
	} else {
		err = a.pluginOriginalNotice.queue.SetPeriods(periods)
	}
	return a.pluginOriginalNotice.queue, err
}

func (a *App) sendOriginalNoticeModules(ctx context.Context, call notice.Call) error {
	_, err := a.sendOriginalNoticeModulesCount(ctx, call)
	return err
}

func (a *App) sendOriginalNoticeModulesCount(ctx context.Context, call notice.Call) (int, error) {
	if len(call.Args) == 0 || call.Args[0] != "post_message" {
		return 0, bad("PLUGIN_NOTIFICATION_INVALID", "通知调用无效")
	}
	kwargs, err := json.Marshal(call.Kwargs)
	if err != nil {
		return 0, err
	}
	args, err := json.Marshal(call.Args[1:])
	if err != nil {
		return 0, err
	}
	var attempts originalModuleAttempts
	_, err = a.invokeOriginalPluginModulesObserved(ctx, "post_message", nil, args, kwargs, &attempts)
	if err != nil {
		return attempts.Called, err
	}
	if attempts.Called == 0 {
		return 0, bad("PLUGIN_NOTIFICATION_PROVIDER_UNAVAILABLE", "没有已启用的通知发送模块")
	}
	if attempts.Failed > 0 {
		return attempts.Called, bad("PLUGIN_NOTIFICATION_SEND_FAILED", "通知发送模块执行失败")
	}
	return attempts.Called, nil
}

func (a *App) runOriginalNoticeQueue(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pluginOriginalNotice.mu.Lock()
			queue := a.pluginOriginalNotice.queue
			a.pluginOriginalNotice.mu.Unlock()
			if queue == nil {
				continue
			}
			periods, err := a.pluginStore.noticePeriods(ctx)
			if err != nil {
				continue
			}
			if err = queue.SetPeriods(periods); err != nil {
				continue
			}
			for _, result := range queue.Tick(ctx) {
				if result.CallbackError != nil {
					a.log("ERROR", "plugins", "notification_failed", "插件通知发送失败", "", "")
				}
			}
		}
	}
}

func (a *App) stopOriginalNoticeQueue() {
	a.pluginOriginalNotice.mu.Lock()
	defer a.pluginOriginalNotice.mu.Unlock()
	if a.pluginOriginalNotice.queue != nil {
		a.pluginOriginalNotice.queue.Stop()
	}
}

func (a *App) originalPostMessageResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	p := scope.object
	if p == nil || p.app != a || scope.phase == "stop" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "通知需要当前插件调用")
	}
	p.sessionMu.Lock()
	concurrent := p.session != nil && p.session.SupportsConcurrent()
	p.sessionMu.Unlock()
	if !concurrent {
		return nil, bad("PLUGIN_CONCURRENT_REQUIRED", "同步通知模块调用需要并发worker")
	}
	var validated map[string]json.RawMessage
	if err := originalResourceDecode(raw, &validated); err != nil {
		return nil, err
	}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, bad("PLUGIN_NOTIFICATION_INVALID", "通知内容必须是对象")
	}
	link, _ := json.Marshal(fields["link"])
	if !originalBaseJSONTruthy(link) {
		fields["link"] = nil
		if domain := a.Settings().AppDomain; domain != "" {
			fields["link"] = strings.TrimRight(domain, "/") + "/#/plugins?tab=installed&id=" + p.installation.InstanceID
		}
	}
	result, err := a.postOriginalPluginMessage(ctx, fields)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (a *App) postOriginalPluginMessage(ctx context.Context, fields map[string]any) (plugins.NotificationDispatch, error) {
	out := plugins.NotificationDispatch{Errors: []string{}}
	if !a.admitOriginalBackground() {
		return out, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	message, err := notice.NewEnvelope(fields)
	if err != nil {
		return out, bad("PLUGIN_NOTIFICATION_INVALID", "通知字段不符合原版格式")
	}
	preference := func(kind, key string) (any, error) { return a.pluginStore.noticePreference(ctx, a.Store.DB, kind, key) }
	superuser, err := preference("superuser", "")
	if err != nil {
		return out, err
	}
	if superuser == nil {
		var username string
		err = a.Store.DB.QueryRowContext(ctx, "SELECT username FROM admin WHERE id=1").Scan(&username)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if username == "" {
			username = "admin"
		}
		superuser = username
	}
	username, ok := superuser.(string)
	if !ok {
		return out, notice.SourceError{Class: "TypeError"}
	}
	router := notice.Router{Dependencies: notice.Dependencies{
		Superuser: username,
		Render: func(ctx context.Context, message *notice.Envelope, _ notice.Business, kwargs notice.Object) (*notice.Envelope, error) {
			result, err := a.pluginStore.noticeTemplateHelper().RenderMessage(ctx, message, noticetemplate.ContextInput{Extra: kwargs}, func(context.Context) (any, error) { return preference("templates", "NotificationTemplates") })
			return result.Message, err
		},
		SaveHistory: func(ctx context.Context, value notice.Object) error {
			text := func(key string) string { s, _ := value[key].(string); return s }
			var userID *string
			switch user := value["userid"].(type) {
			case string:
				userID = &user
			case json.Number:
				s := user.String()
				userID = &s
			}
			action, err := json.Marshal(value["action"])
			if err != nil {
				return err
			}
			n := Notification{ID: id("notice_"), Source: text("source"), Title: text("title"), Message: text("text"), MType: text("mtype"), Link: text("link"), Channel: text("channel"), Image: text("image"), UserID: userID, Action: action, Note: json.RawMessage(`{}`), CreatedAt: now()}
			record, err := a.originalMessageHistoryRecord(n, value)
			if err != nil {
				return err
			}
			record.Notification = Notification{ID: n.ID, CreatedAt: n.CreatedAt, Read: n.Read}
			return a.Store.Put(ctx, "notification", n.ID, record)
		},
		NotificationSwitch: func(_ context.Context, key any) (any, error) {
			s, ok := key.(string)
			if !ok {
				return nil, notice.SourceError{Class: "AttributeError"}
			}
			return preference("switch", s)
		},
		UserSettings: func(_ context.Context, key any) (any, error) {
			s, ok := key.(string)
			if !ok {
				return nil, notice.SourceError{Class: "TypeError"}
			}
			return preference("user", s)
		},
		NoticeMessage: func(ctx context.Context, value notice.Object) error {
			raw, err := json.Marshal(value)
			if err == nil {
				a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), "notice.message", raw)
			}
			return err
		},
		QueueCall: func(ctx context.Context, call notice.Call) error {
			queue, err := a.originalNoticeQueue(ctx)
			if err != nil {
				return err
			}
			attempt, err := queue.Send(ctx, call)
			if err != nil {
				return err
			}
			if attempt.Queued {
				out.Queued++
			}
			if attempt.Attempted {
				out.Attempted++
			}
			if attempt.CallbackError != nil {
				a.logOriginalPluginContext(ctx, "ERROR", "notification_failed", "插件通知发送失败")
				out.Failed++
				code := pluginErrorCodeInternal(attempt.CallbackError)
				if code == "" {
					code = "PLUGIN_NOTIFICATION_SEND_FAILED"
				}
				out.Errors = append(out.Errors, code)
			}
			return nil
		},
	}}
	err = router.Post(ctx, message, notice.Business{}, nil)
	return out, err
}
