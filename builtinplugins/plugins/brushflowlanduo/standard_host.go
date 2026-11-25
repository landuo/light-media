package brushflowlanduo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	sdk "light-media/builtinplugins/internal/sdk"
)

type brushHostKey struct{}

func withBrushHost(ctx context.Context, caller sdk.HostCaller) context.Context {
	return context.WithValue(ctx, brushHostKey{}, caller)
}

func brushCaller(ctx context.Context) sdk.HostCaller {
	caller, _ := ctx.Value(brushHostKey{}).(sdk.HostCaller)
	return caller
}

// Only cached subscription aliases survive a call. Every host grant is
// resolved through the incoming context, including a nested get_service call.
type standardHost struct {
	aliasesMu sync.Mutex
	aliases   map[string][]string
}

func (h *standardHost) resetAliases() {
	h.aliasesMu.Lock()
	defer h.aliasesMu.Unlock()
	h.aliases = nil
}

func (*standardHost) Now() time.Time {
	at := time.Now()
	zone, configured := os.LookupEnv("TZ")
	if !configured {
		zone = "Asia/Shanghai"
	}
	if location, err := time.LoadLocation(zone); err == nil && zone != "" {
		at = at.In(location)
	}
	return at.Truncate(time.Microsecond)
}

func (*standardHost) NewID() string {
	var raw [16]byte
	rand.Read(raw[:])
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	return hex.EncodeToString(raw[:])
}

func (*standardHost) RandomTag() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var result [10]byte
	for i := range result {
		var sample [1]byte
		for {
			rand.Read(sample[:])
			if sample[0] < 248 {
				result[i] = alphabet[int(sample[0])%len(alphabet)]
				break
			}
		}
	}
	return string(result[:])
}

func (*standardHost) Sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (*standardHost) Get(ctx context.Context, key string) (any, error) {
	raw, err := (sdk.Base{Host: brushCaller(ctx)}).GetData(ctx, &key)
	if err != nil {
		return nil, err
	}
	// The source record store orders dictionaries by insertion, so object
	// bytes must reach RecordSet before ordinary map decoding.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed) {
		return append(json.RawMessage(nil), trimmed...), nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid BrushFlow Base data")
	}
	return value, nil
}

func (*standardHost) Put(ctx context.Context, key string, value any) error {
	return (sdk.Base{Host: brushCaller(ctx)}).SaveData(ctx, key, value)
}

func (*standardHost) Del(ctx context.Context, key string) error {
	return (sdk.Base{Host: brushCaller(ctx)}).DelData(ctx, &key)
}

func (*standardHost) SaveConfig(ctx context.Context, value Row) error {
	_, err := (sdk.Base{Host: brushCaller(ctx)}).UpdateConfig(ctx, value)
	return err
}

func (*standardHost) RefreshSchedules(ctx context.Context, _ []Service) error {
	return (sdk.Manager{Host: brushCaller(ctx)}).RefreshServices(ctx)
}

func (*standardHost) Queue(ctx context.Context, task Task, operation string) error {
	args := Row{"task_id": task.ID()}
	if operation == "cleanup_task_tag" {
		args["task"] = task.Row
	}
	return (sdk.Manager{Host: brushCaller(ctx)}).Submit(ctx, operation, args)
}

func (*standardHost) Message(ctx context.Context, notice Notice) error {
	caller := brushCaller(ctx)
	if notice.System {
		raw, err := json.Marshal(Row{"title": notice.Title, "message": notice.Text})
		if err != nil {
			return err
		}
		_, err = caller(ctx, sdk.SystemMessagePutCapability, raw)
		return err
	}
	_, err := (sdk.Base{Host: caller}).PostMessage(ctx, Row{"mtype": "站点", "title": notice.Title, "text": notice.Text})
	return err
}

func (*standardHost) Event(ctx context.Context, event Event) error {
	caller := brushCaller(ctx)
	instance, err := (sdk.Base{Host: caller}).GetInstanceMetadata(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(Row{"plugin_id": instance.InstanceID, "event_name": event.Name, "hash": event.Hash,
		"data": event.Data, "downloader": event.Downloader})
	if err != nil {
		return err
	}
	_, err = sdk.SendEvent(ctx, caller, sdk.EventSendRequest{Kind: sdk.BroadcastEventKind, Type: "plugin.triggered", Data: raw})
	return err
}

func (*standardHost) Log(ctx context.Context, level, message string) {
	limit := sdk.LogMessageLimit
	if len(message) > limit {
		for message[limit]&0xc0 == 0x80 {
			limit--
		}
		message = message[:limit]
	}
	_ = (sdk.Logger{Host: brushCaller(ctx)}).Log(ctx, sdk.LogLevel(strings.ToUpper(level)), message)
}

func (h *standardHost) SubscribeTitles(ctx context.Context) ([]string, error) {
	caller := brushCaller(ctx)
	result, err := (sdk.Subscriptions{Host: caller}).Read(ctx, sdk.SubscriptionHostRequestV1{})
	if err != nil {
		return nil, err
	}
	h.aliasesMu.Lock()
	defer h.aliasesMu.Unlock()
	next := make(map[string][]string, len(result.Items))
	out := []string{}
	seen := map[string]bool{}
	cache := true
	for _, raw := range result.Items {
		subscription, err := standardObject(raw)
		if err != nil {
			return nil, err
		}
		name := text(subscription["name"])
		key := text(subscription["id"]) + "_" + name
		names, cached := h.aliases[key]
		if !cached {
			names = []string{name}
			kind := text(subscription["type"])
			meta := map[string]any{"original_name": name, "type": kind, "year": subscription["year"]}
			if truth(subscription["season"]) {
				meta["begin_season"] = subscription["season"]
			}
			media, err := (sdk.MediaHost{Host: caller}).Recognize(ctx, sdk.MediaRecognizeRequest{
				Meta: meta, Type: kind, TMDBID: int64(number(subscription["tmdbid"])), DoubanID: text(subscription["doubanid"]), Cache: &cache,
			})
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				h.Log(ctx, "error", "识别订阅 "+name+" 失败")
			} else if result, err := standardObject(media); err == nil {
				for _, alias := range list(result["names"]) {
					names = append(names, text(alias))
				}
			}
		}
		next[key] = names
		for _, alias := range names {
			alias = strings.TrimSpace(alias)
			if alias != "" && !seen[alias] {
				out = append(out, alias)
				seen[alias] = true
			}
		}
	}
	h.aliases = next
	return out, nil
}
