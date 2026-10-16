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
	if Truth(e.StartTime) && Truth(e.EndTime) {
		start, sok := numeric(e.StartTime)
		end, eok := numeric(e.EndTime)
		if !sok || !eok {
			return errors.New("source hour window comparison requires numeric boundaries")
		}
		hour, _ := numeric(today.Hour())
		if hour.Cmp(start) < 0 || hour.Cmp(end) > 0 {
			return nil
		}
	}
	var sourceEvent *SourceEvent
	if event != nil {
		sourceEvent = &SourceEvent{Channel: event.Data["channel"], User: event.Data["user"]}
		if h.Run.Notify == nil {
			return errors.New("event notification sink not ready")
		}
		if err := h.Run.Notify(ctx, Object{"channel": sourceEvent.Channel, "title": "开始站点签到 ...", "userid": sourceEvent.User}); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		operation string
		selected  any
	}{{"签到", e.SignSites}, {"登录", e.LoginSites}} {
		if !Truth(item.selected) {
			continue
		}
		if h.HistoryKeys == nil {
			return errors.New("history key snapshot not ready")
		}
		if err := do(ctx, today, item.operation, item.selected, sourceEvent, h.HistoryKeys(), h.Run); err != nil {
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
	r, err := h.Visit(ctx, site)
	if err != nil {
		return nil, err
	}
	return Object{"success": r.Success, "message": fmt.Sprintf("站点【%s】%s", r.SiteName, r.Message), "data": Object{"signin_success": r.Success, "login_success": r.LoggedIn}}, nil
}
