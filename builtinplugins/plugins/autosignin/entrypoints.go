package autosignin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EventEnvelope is host-delivered. The host still enforces the source lifecycle
// event-enabled state; direct service/API method calls do not invent that guard.
type EventEnvelope struct{ Data Object }
type EntryHooks struct {
	Run         RunHooks
	HistoryKeys func() []string
}

func (e *Engine) SignIn(ctx context.Context, event *EventEnvelope, h EntryHooks) error {
	return e.signIn(ctx, event, h, e.Do)
}
func (e *Engine) signIn(ctx context.Context, event *EventEnvelope, h EntryHooks, do func(context.Context, time.Time, string, any, *SourceEvent, []string, RunHooks) error) error {
	if event != nil && (!Truth(event.Data) || event.Data["action"] != "site_signin") {
		return nil
	}
	today := e.Now()
	if Truth(e.field("StartTime")) && Truth(e.field("EndTime")) {
		hour, _ := numeric(e.Now().Hour())
		start, sok := numeric(e.field("StartTime"))
		if !sok {
			return errors.New("source hour window comparison requires numeric boundaries")
		}
		outside := hour.Cmp(start) < 0
		if !outside {
			hour, _ = numeric(e.Now().Hour())
			end, ok := numeric(e.field("EndTime"))
			if !ok {
				return errors.New("source hour window comparison requires numeric boundaries")
			}
			outside = hour.Cmp(end) > 0
		}
		if outside {
			e.log("ERROR", fmt.Sprintf("当前时间 %d 不在 %s-%s 范围内，暂不执行任务", e.Now().Hour(), pythonScalar(e.field("StartTime")), pythonScalar(e.field("EndTime"))))
			return nil
		}
	}
	var sourceEvent *SourceEvent
	if event != nil {
		e.log("INFO", "收到命令，开始站点签到 ...")
		sourceEvent = &SourceEvent{Channel: event.Data["channel"], User: event.Data["user"]}
		if h.Run.Notify == nil {
			return errors.New("event notification sink not ready")
		}
		if err := h.Run.Notify(ctx, Object{"channel": sourceEvent.Channel, "title": "开始站点签到 ...", "userid": sourceEvent.User}); err != nil {
			return err
		}
	}
	for _, item := range []struct{ operation, field string }{{"签到", "SignSites"}, {"登录", "LoginSites"}} {
		if !Truth(e.field(item.field)) {
			continue
		}
		selected := e.field(item.field)
		if h.HistoryKeys == nil {
			return errors.New("history key snapshot not ready")
		}
		if err := do(ctx, today, item.operation, selected, sourceEvent, h.HistoryKeys(), h.Run); err != nil {
			return err
		}
	}
	return nil
}

// Test-only synthetic API keys are used in the AST reference. A real host checks
// its secret once, passes this boolean, and resolves a public domain to its own
// trusted current site snapshot. Neither key nor URL authorizes a transport.
type DomainAPIHooks struct {
	KeyAccepted bool
	Lookup      func(context.Context, string) (Object, error)
	Visit       func(context.Context, Object) (Result, error)
}

func SigninByDomain(ctx context.Context, address string, h DomainAPIHooks) (Object, error) {
	if !h.KeyAccepted {
		return Object{"success": false, "message": "API密钥错误"}, nil
	}
	if h.Lookup == nil {
		return nil, errors.New("site lookup not ready")
	}
	site, err := h.Lookup(ctx, sourceDomain(address))
	if err != nil {
		return nil, err
	}
	if !Truth(site) {
		return Object{"success": false, "message": fmt.Sprintf("站点【%s】不存在", address)}, nil
	}
	if h.Visit == nil {
		return nil, ErrAdapterNotReady
	}
	r, err := h.Visit(withCacheDumpEntry(ctx, sourceCacheAPI), site)
	if err != nil {
		return nil, err
	}
	return Object{"success": r.Success, "message": fmt.Sprintf("站点【%s】%s", pythonScalar(r.SiteName), r.Message), "data": Object{"signin_success": r.Success, "login_success": r.LoggedIn}}, nil
}
