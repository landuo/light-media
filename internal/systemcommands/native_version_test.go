package systemcommands

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type nativeVersionMarker struct {
	value       any
	present     bool
	calls       []string
	deleteError error
}

func (p *nativeVersionMarker) Get(context.Context, string) (any, bool, error) {
	p.calls = append(p.calls, "get")
	return p.value, p.present, nil
}
func (p *nativeVersionMarker) Set(context.Context, string, any) error {
	panic("version/finish must not write marker")
}
func (p *nativeVersionMarker) Delete(context.Context, string) error {
	p.calls = append(p.calls, "delete")
	if p.deleteError != nil {
		return p.deleteError
	}
	p.present = false
	return nil
}
func (p *nativeVersionMarker) Encode(context.Context, any) (any, error) {
	panic("version/finish must not encode marker")
}
func (p *nativeVersionMarker) Decode(_ context.Context, v any) (any, error) {
	p.calls = append(p.calls, "decode")
	return v, nil
}
func TestNativeVersionAndRestartFinishOrder(t *testing.T) {
	ctx := context.Background()
	marker := &nativeVersionMarker{value: map[string]any{"channel": "Web", "userid": "owned-user"}, present: true}
	remote := "v0.4.19"
	notices := []Notice{}
	noticeError := error(nil)
	version, err := NewNativeVersion("__system_restart__", []Channel{{Name: "Web", Value: "Web"}}, NativeVersionPorts{
		Latest: func(context.Context) (*string, error) {
			marker.calls = append(marker.calls, "latest")
			return &remote, nil
		},
		LocalBackend: func(context.Context) (string, error) {
			marker.calls = append(marker.calls, "backend")
			return "0.4.19", nil
		},
		LocalFrontend: func(context.Context) (string, error) {
			marker.calls = append(marker.calls, "frontend")
			return "0.4.18", nil
		},
		Notify: func(_ context.Context, n Notice) error {
			marker.calls = append(marker.calls, "notice")
			notices = append(notices, n)
			return noticeError
		},
		Log: func(context.Context, string, string) error { return nil }, Cache: marker, Codec: marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := "owned-web"
	channel := &Channel{Name: "Web", Value: "Web"}
	if err = version.Version(ctx, channel, "owned-user", &source); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(marker.calls, []string{"latest", "backend", "frontend", "notice"}) {
		t.Fatal(marker.calls)
	}
	if n := notices[0]; n.Channel != channel || n.UserID != "owned-user" || !n.IncludeSource || n.Source != &source || n.SaveHistory {
		t.Fatal("version routing altered")
	}
	marker.calls = nil
	if err = version.RestartFinish(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(marker.calls, []string{"get", "decode", "latest", "backend", "frontend", "notice", "delete"}) || marker.present {
		t.Fatal(marker.calls)
	}
	if n := notices[1]; n.IncludeSource || n.Source != nil || n.SaveHistory || n.Channel == nil || n.Channel.Value != "Web" || n.UserID != "owned-user" || n.Title != "系统已重启完成！\n"+notices[0].Title {
		t.Fatal("finish routing/title altered")
	}
	marker.calls = nil
	if err = version.RestartFinish(ctx); err != nil || !reflect.DeepEqual(marker.calls, []string{"get"}) {
		t.Fatal("absent marker fetched remote", err, marker.calls)
	}
	marker.present = true
	noticeError = errors.New("owned notice failure")
	marker.calls = nil
	if err = version.RestartFinish(ctx); !errors.Is(err, noticeError) || !marker.present || marker.calls[len(marker.calls)-1] != "notice" {
		t.Fatal("notice failure deleted marker")
	}
	noticeError = nil
	marker.deleteError = errors.New("owned delete failure")
	marker.calls = nil
	if err = version.RestartFinish(ctx); !errors.Is(err, marker.deleteError) || !marker.present {
		t.Fatal("delete failure removed marker")
	}
	marker.deleteError = nil
	marker.calls = nil
	before := len(notices)
	if err = version.RestartFinish(ctx); err != nil || marker.present || len(notices) != before+1 {
		t.Fatal("explicit later finish retry not retained")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	marker.calls = nil
	if err = version.Version(canceled, channel, "owned-user", &source); !errors.Is(err, context.Canceled) || len(marker.calls) != 0 {
		t.Fatal("canceled version effects")
	}
}
