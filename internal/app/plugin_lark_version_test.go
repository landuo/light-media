package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
)

// Preexisting App APIs make this a real before/after consumer probe.
func TestPluginLarkActualVersionReleasesNoticeAndKnownReplay(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	settings := []byte(`{"Version":1,"Known":true,"GitHubHeaders":{"Authorization":"Bearer owned-version","User-Agent":"Owned source UA"},"Proxy":null,"Environment":[]}`)
	sealed, e := a.pluginStore.seal("source_network_settings", "system", "main", settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('source_network_settings','main',?,?)`, sealed, now()); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer owned-version" || r.Header.Get("User-Agent") != "Owned source UA" {
			t.Error("source request fields changed", r.Method, r.Host)
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/repos/landuo/MoviePilot/releases":
			fmt.Fprint(w, `[{"tag_name":"v2.9.99"},{"tag_name":"v3.0.0"},{"tag_name":"v2.12.1"},{"tag_name":"v2.10.7"}]`)
		case "/repos/jxxghp/MoviePilot-Frontend/releases":
			fmt.Fprint(w, `[{"tag_name":"v2.9.7"},{"tag_name":"v2.11.2"}]`)
		default:
			t.Error("unexpected source endpoint", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	old := http.DefaultTransport
	base := old.(*http.Transport).Clone()
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api.github.com:443" {
			return nil, errors.New("owned version test refuses every other authority")
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	http.DefaultTransport = base
	t.Cleanup(func() { http.DefaultTransport = old; base.CloseIdleConnections() })
	sender := installNoticeSourceFixtures(t, a, catalog.Media{}, 0, 0)
	var lost atomic.Bool
	a.pluginStore.notices.afterCommit = func(phase string) error {
		if phase == "queue_finish" && lost.CompareAndSwap(false, true) {
			return errors.New("owned version finish Commit ACK lost")
		}
		return nil
	}
	if e = a.larkRouteCore(ctx, core, nil, "/version ignored arguments", "ou_owned", "", ""); e != nil {
		t.Fatal("real version consumer did not execute", e)
	}
	if calls.Load() != 2 || sender.Sends.Load() != 1 || !lost.Load() {
		t.Fatal("source release calls/Notice missing", calls.Load(), sender.Sends.Load(), lost.Load())
	}
	route := noticeV2OnlyRoute(t, a)
	render, e := a.pluginStore.noticeBusinessRender(ctx, route.ID)
	if e != nil {
		t.Fatal(e)
	}
	f := render.Snapshot.Fields
	title, _ := f["title"].(string)
	if !strings.Contains(title, "当前后端版本："+appVersion+"，远程版本：v2.12.1") || !strings.Contains(title, "当前前端版本：") || !strings.HasSuffix(title, "远程版本：v2.11.2") || f["save_history"] != false || f["text"] != nil || f["mtype"] != nil {
		t.Fatal("source version message changed", f)
	}
	core.ordinal = 0
	// Historical read/Notice facts stay usable without rereading later settings.
	if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='source_network_settings'`); e != nil {
		t.Fatal(e)
	}
	if e = a.larkRouteCore(ctx, core, nil, "/version ignored arguments", "ou_owned", "", ""); e != nil || calls.Load() != 2 || sender.Sends.Load() != 1 {
		t.Fatal("known version replay queried/sent again", e, calls.Load(), sender.Sends.Load())
	}
	dir := a.Dir
	core.release()
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	got, _, e := cold.pluginStore.readNoticeRoute(context.Background(), cold.Store.DB, route.ID)
	if e != nil || !got.Complete || calls.Load() != 2 || sender.Sends.Load() != 1 {
		t.Fatal("cold source version fact changed", e)
	}
}

func TestPluginLarkVersionSourceFailureAndNewRequestAdmission(t *testing.T) {
	for _, kind := range []string{"http500", "broken-json", "settings-missing", "stopped-before-second"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
			core, ctx := larkStartedCoreFixture(t, a)
			if kind != "settings-missing" {
				raw := []byte(`{"Version":1,"Known":true,"GitHubHeaders":{},"Proxy":null,"Environment":[]}`)
				sealed, e := a.pluginStore.seal("source_network_settings", "system", "main", raw)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('source_network_settings','main',?,?)`, sealed, now()); e != nil {
					t.Fatal(e)
				}
			}
			var calls atomic.Int64
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Host != "" || r.Host != "api.github.com" {
					t.Error("owned official authority projection changed")
				}
				if kind == "http500" {
					w.WriteHeader(500)
					fmt.Fprint(w, `[{"tag_name":"v2.99"}]`)
					return
				}
				if kind == "stopped-before-second" {
					if _, e := a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE plugin_id='LarkMessager'`); e != nil {
						t.Error(e)
					}
				}
				if kind == "broken-json" && strings.Contains(r.URL.Path, "/landuo/") {
					fmt.Fprint(w, `{`)
					return
				}
				fmt.Fprint(w, `[{"tag_name":"v2.8"}]`)
			}))
			defer srv.Close()
			old := http.DefaultTransport
			base := old.(*http.Transport).Clone()
			base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "api.github.com:443" {
					return nil, errors.New("owned test refuses all other dials")
				}
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			}
			http.DefaultTransport = base
			t.Cleanup(func() { http.DefaultTransport = old; base.CloseIdleConnections() })
			sender := installNoticeSourceFixtures(t, a, catalog.Media{}, 0, 0)
			e := a.larkRouteCore(ctx, core, nil, "/version", "ou_owned", "", "")
			if kind == "settings-missing" {
				if pluginErrorCodeInternal(e) != "LARK_VERSION_SETTINGS_DEPENDENCY" || calls.Load() != 0 || sender.Sends.Load() != 0 {
					t.Fatal("unknown source settings became None/sent/requested", e, calls.Load(), sender.Sends.Load())
				}
				return
			}
			if kind == "stopped-before-second" {
				if e == nil || calls.Load() != 1 || sender.Sends.Load() != 0 {
					t.Fatal("new external request ignored current source scope", e, calls.Load(), sender.Sends.Load())
				}
				return
			}
			if e != nil || calls.Load() != 2 || sender.Sends.Load() != 1 {
				t.Fatal("caught source failure aborted actual normal post", e, calls.Load(), sender.Sends.Load())
			}
			route := noticeV2OnlyRoute(t, a)
			render, e := a.pluginStore.noticeBusinessRender(ctx, route.ID)
			if e != nil {
				t.Fatal(e)
			}
			title, _ := render.Snapshot.Fields["title"].(string)
			if !strings.Contains(title, "当前后端版本："+appVersion+"，远程版本：None") {
				t.Fatal("source None changed", title)
			}
			if kind == "http500" && !strings.HasSuffix(title, "远程版本：None") {
				t.Fatal(title)
			}
			if kind == "broken-json" && !strings.HasSuffix(title, "远程版本：v2.8") {
				t.Fatal(title)
			}
		})
	}
}
