package notice

import (
	"context"
	"errors"
	"strings"
	"time"

	"light-media/internal/releasemeta/legacyanime"
)

var ErrTemplateDependency = errors.New("notification template renderer is not ready")
var ErrDependency = errors.New("notification trusted dependency is not configured")
var ErrInvalidEnvelope = errors.New("notification envelope must be constructed with NewEnvelope")

// Business carries host-side objects only. No template object projection or
// Jinja equivalence is implied; the renderer has to implement that separately.
type Business struct{ Meta, MediaInfo, TorrentInfo, TransferInfo any }
type Dependencies struct {
	Now                func() time.Time
	Superuser          string
	Render             func(context.Context, *Envelope, Business, Object) (*Envelope, error)
	SaveHistory        func(context.Context, Object) error
	NotificationSwitch func(context.Context, any) (any, error)
	UserSettings       func(context.Context, any) (any, error)
	NoticeMessage      func(context.Context, Object) error
	QueueCall          func(context.Context, Call) error
}
type Router struct{ Dependencies }

func (r Router) Post(ctx context.Context, message *Envelope, business Business, kwargs Object) error {
	if message != nil && message.fields == nil {
		return ErrInvalidEnvelope
	}
	v, err := jsonCopy(kwargs)
	if err != nil {
		return err
	}
	kw, _ := v.(map[string]any)
	if kw == nil {
		kw = Object{}
	}
	// These names belong to the Python function signature, never **kwargs.
	for _, reserved := range []string{"message", "meta", "mediainfo", "torrentinfo", "transferinfo"} {
		if _, exists := kw[reserved]; exists {
			return SourceError{"TypeError"}
		}
	}
	if _, exists := kw["current_time"]; !exists {
		now := time.Now
		if r.Now != nil {
			now = r.Now
		}
		kw["current_time"] = now().Format("2006-01-02 15:04:05")
	}
	message = message.clone()
	if r.Render != nil {
		message, err = r.Render(ctx, message, business, owned(kw).(map[string]any))
		if err != nil {
			return err
		}
	} else if message != nil && !truth(message.fields["title"]) && !truth(message.fields["text"]) && truth(message.fields["ctype"]) {
		return ErrTemplateDependency
	}
	if message == nil {
		return nil
	}
	if message.fields == nil {
		return ErrInvalidEnvelope
	}
	if message.RequiresNativeEnumRouting() {
		// Plain JSON queue calls cannot preserve an enum replaced by an
		// unvalidated Python value. Do not send a subtly revalidated message.
		return ErrTemplateDependency
	}
	if truth(message.fields["save_history"]) {
		if r.SaveHistory == nil {
			return ErrDependency
		}
		if err := r.SaveHistory(ctx, message.Dump()); err != nil {
			return err
		}
	}
	dispatch := message.clone()
	if id, ok := dispatch.fields["userid"].(string); ok && legacyanime.LowerCase(legacyanime.Strip(id)) == "system" {
		dispatch.fields["userid"] = nil
	}
	emit := func(m *Envelope, immediately *bool) error {
		if r.NoticeMessage == nil {
			return ErrDependency
		}
		data := m.Dump()
		delete(data, "save_history")
		data["type"] = data["mtype"]
		if err := r.NoticeMessage(ctx, data); err != nil {
			return err
		}
		if r.QueueCall == nil {
			return ErrDependency
		}
		args := []any{"post_message"}
		fields := owned(kw).(map[string]any)
		// Python raises duplicate-keyword TypeError after sending NoticeMessage.
		if _, ok := fields["message"]; ok {
			return SourceError{"TypeError"}
		}
		fields["message"] = m.Dump()
		if immediately != nil {
			if _, ok := fields["immediately"]; ok {
				return SourceError{"TypeError"}
			}
			fields["immediately"] = *immediately
		}
		return r.QueueCall(ctx, Call{args, fields})
	}
	if !truth(dispatch.fields["userid"]) && truth(dispatch.fields["mtype"]) {
		if r.NotificationSwitch == nil {
			return ErrDependency
		}
		action, err := r.NotificationSwitch(ctx, dispatch.Get("mtype"))
		if err != nil {
			return err
		}
		if truth(action) {
			actions, ok := action.(string)
			if !ok {
				return SourceError{"AttributeError"}
			}
			adminSent, sendOriginal := false, false
			settings := func(key any) (any, error) {
				if r.UserSettings == nil {
					return nil, ErrDependency
				}
				value, err := r.UserSettings(ctx, key)
				if err != nil {
					return nil, err
				}
				return jsonCopy(value)
			}
			for _, a := range strings.Split(actions, ",") {
				m := dispatch.clone()
				if a == "admin" && !adminSent {
					targets, err := settings(r.Superuser)
					if err != nil {
						return err
					}
					m.fields["targets"] = targets
					adminSent = true
				} else if a == "user" && truth(m.fields["username"]) {
					targets, err := settings(m.Get("username"))
					if err != nil {
						return err
					}
					m.fields["targets"] = targets
					if targets == nil {
						if !adminSent {
							targets, err = settings(r.Superuser)
							if err != nil {
								return err
							}
							m.fields["targets"] = targets
							adminSent = true
						} else {
							continue
						}
					} else if name, ok := m.fields["username"].(string); ok && name == r.Superuser {
						adminSent = true
					}
				} else {
					if !adminSent {
						sendOriginal = true
					}
					break
				}
				if err := emit(m, nil); err != nil {
					return err
				}
			}
			if !sendOriginal {
				return nil
			}
		}
	}
	immediate := truth(dispatch.fields["userid"])
	return emit(dispatch, &immediate)
}
