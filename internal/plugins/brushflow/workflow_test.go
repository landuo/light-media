package brushflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type unknownEffect struct{}

func (unknownEffect) Error() string             { return "owned dispatch lost acknowledgement" }
func (unknownEffect) StopExternalEffects() bool { return true }

func promotionFixture(t *testing.T, expired bool) (*Core, *memoryHost, Task) {
	t.Helper()
	h := testHost()
	task := NormalizeTask(Row{"id": "promotion", "site_id": 1, "downloader": "qb", "enabled": true, "notify": false, "del_no_free": true, "delete_except_tags": "keep"}, h.NewID)
	p := newCore(h, Config{Settings: Row{"enabled": true}, Tasks: []Task{task}})
	h.torrents["qb"] = []DownloadState{{Kind: "qbittorrent", Raw: Row{"hash": "owned", "name": "Owned", "total_size": 100, "downloaded": 10, "progress": .1, "tags": GlobalTag + "," + task.Tag() + ",keep", "added_on": h.now.Unix() - 3600}}}
	r := Row{"title": "Owned", "hit_and_run": true, "size": 100, "promotion_probe_at": float64(h.now.Unix())}
	if expired {
		r["promotion_ended_at"] = float64(h.now.Unix())
	}
	rs := &RecordSet{Values: Rows{}}
	rs.Set("owned", r)
	if e := p.put(context.Background(), task.ID(), "torrents", rs); e != nil {
		t.Fatal(e)
	}
	return p, h, task
}

func TestSourcePromotionProtectionWorkflow(t *testing.T) {
	for _, kind := range []string{"unknown_pause", "known_pause_false", "expired_hr_excluded"} {
		t.Run(kind, func(t *testing.T) {
			p, h, task := promotionFixture(t, kind == "expired_hr_excluded")
			if kind == "unknown_pause" {
				h.pauseErr = unknownEffect{}
			}
			if kind == "known_pause_false" {
				h.pauseResult = false
			}
			e := p.Check(context.Background(), task.ID(), true)
			if kind == "unknown_pause" {
				var unknown unknownEffect
				if !errors.As(e, &unknown) {
					t.Fatalf("unknown pause must stop the source workflow: %v", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			ops := []string{}
			for _, a := range h.actions {
				ops = append(ops, text(a["op"]))
			}
			if kind == "unknown_pause" {
				if strings.Join(ops, ",") != "pause" {
					t.Fatal("unknown cannot trigger fallback delete or later IO", ops)
				}
			} else if len(ops) < 2 || ops[0] != "pause" || ops[1] != "delete" || contains(ops, "reannounce") {
				t.Fatal("promotion safeguard order/protected reannounce", ops)
			}
		})
	}
}

func TestSourceGlobalDeleteSharedPhysicalTorrentProtection(t *testing.T) {
	for _, protected := range []bool{true, false} {
		t.Run(map[bool]string{true: "paused_association", false: "both_eligible"}[protected], func(t *testing.T) {
			h := testHost()
			a := NormalizeTask(Row{"id": "a", "downloader": "qb", "enabled": true, "notify": false, "proxy_delete": true}, h.NewID)
			b := NormalizeTask(Row{"id": "b", "downloader": "qb", "enabled": !protected, "notify": false, "proxy_delete": true}, h.NewID)
			p := newCore(h, Config{Settings: Row{"enabled": true, "global_proxy_delete": true, "global_delete_size_range": "1-2"}, Tasks: []Task{a, b}})
			h.torrents["qb"] = []DownloadState{{Kind: "qbittorrent", Raw: Row{"hash": "shared", "name": "Shared", "total_size": float64(3 << 30), "downloaded": float64(3 << 30), "progress": 1, "completion_on": h.now.Unix() - 7200}}}
			for _, task := range []Task{a, b} {
				rs := &RecordSet{Values: Rows{}}
				rs.Set("shared", Row{"title": "Shared", "size": float64(3 << 30)})
				if e := p.put(context.Background(), task.ID(), "torrents", rs); e != nil {
					t.Fatal(e)
				}
			}
			n, e := p.GlobalDelete(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if protected {
				if n != 0 || len(h.actions) != 0 {
					t.Fatal("disabled association must protect the shared physical torrent", n, h.actions)
				}
			} else {
				if n != 1 || len(h.actions) != 2 || h.actions[0]["op"] != "reannounce" || h.actions[1]["op"] != "delete" {
					t.Fatal("one physical delete for two eligible associations", n, h.actions)
				}
				for _, task := range []Task{a, b} {
					rs, e := p.records(context.Background(), task.ID(), "torrents")
					if e != nil || !truth(rs.Values["shared"]["deleted"]) {
						t.Fatal("both associations must publish deletion", e, rs)
					}
				}
			}
		})
	}
}
