package brushflowlanduo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

type nexusDetailTestHost struct {
	*promotionHost
	site       Site
	page       string
	url        string
	listing    string
	listReads  int
	detailErr  error
	listFilter string
	listErr    error
}

func (h *nexusDetailTestHost) Sites(context.Context) ([]Site, error) {
	return []Site{h.site}, nil
}

func (h *nexusDetailTestHost) SiteRequest(_ context.Context, siteID int, method, raw string, _ Row) (Row, error) {
	if siteID != h.site.ID || method != "GET" {
		return nil, fmt.Errorf("unexpected %s request for site %d", method, siteID)
	}
	if raw == h.url {
		return Row{"ok": true, "status": 200, "text": h.page, "url": raw}, h.detailErr
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != h.site.Domain || u.Path != "/torrents.php" ||
		u.Query().Get("search") != "Test Film" {
		return nil, fmt.Errorf("unexpected listing request: %s", raw)
	}
	h.listReads++
	h.listFilter = u.Query().Get("spstate")
	return Row{"ok": true, "status": 200, "text": h.listing, "url": raw}, h.listErr
}

func nexusDetailFixture(t *testing.T, domain string, now time.Time) (*Core, *nexusDetailTestHost, Task, Row) {
	t.Helper()
	task := promotionTask()
	site := Site{ID: task.SiteID(), Domain: domain, URL: "https://" + domain}
	host := &nexusDetailTestHost{
		promotionHost: &promotionHost{now: now},
		site:          site, url: site.URL + "/details.php?id=157",
	}
	record := Row{"title": "Test Film", "page_url": host.url, "downloadvolumefactor": 0}
	return NewUninitialized(host), host, task, record
}

const nexusFreeTitle = `<b>[<font class="free">免费</font>]</b>`
const nexusApproval = `<b>(<font class="twoupfree">通过</font>)</b>`
const nexusOtherVersions = `<div id="kothercopy"><table><tr><td><img class="pro_free" alt="Free" title="免费"></td></tr></table></div>`
const nexusBoxPolicy = `<div class="seed-box-policy-detail"><span class="seed-box-policy-promotion-detail-item"><span class="seed-box-policy-status seed-box-policy-download is-blocked">100%</span>无优惠（2026-12-08 10:02 后恢复正常促销）</span></div>`

func nexusDetailHTML(downloadID, titleHTML, banner string) string {
	return `<html><head><title>种子详情 "Test Film" - Powered by NexusPHP</title></head><body><table><tr><td id="outer">` +
		banner + `<h1 id="top">Test Film ` + titleHTML + `</h1>` +
		`<a href="download.php?id=` + downloadID + `">下载种子</a>` +
		nexusBoxPolicy + nexusOtherVersions + `</td></tr></table></body></html>`
}

func nexusCountdown(when, remaining string) string {
	return `<font><b>剩余时间：<span title="` + when + `">` + remaining + `</span></b></font>`
}

func nexusGlobalFree(start, end string) string {
	return `<div><b>全局优惠生效：FREE</b><br>优惠开始：` + start + ` (GMT+08:00)<br>优惠截止：` +
		end + ` (GMT+08:00)<br><a href="/free.php">查看FREE计划/View Free Plan</a></div>`
}

func expectNexusDeadline(t *testing.T, core *Core, task Task, record Row, want time.Time) {
	t.Helper()
	got, _ := core.resolveExpiry(task, record)
	if got == nil || !got.Equal(want) || !truth(record["promotion_verified_at"]) {
		t.Fatalf("deadline=%v verified=%v, want %s", got, record["promotion_verified_at"], want)
	}
}

func TestNexusDetailPromotionUBitsTitle(t *testing.T) {
	siteTime := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 11, 25, 9, 55, 0, 0, siteTime)
	core, host, task, record := nexusDetailFixture(t, "ubits.club", now)
	title := nexusFreeTitle + nexusCountdown("2026-11-25 10:40:00", "45分钟") + nexusApproval
	host.page = nexusDetailHTML("157", title, "")
	ok, err := core.verifyCandidate(context.Background(), task, record)
	if err != nil || !ok {
		t.Fatalf("verified=%t err=%v for the selected torrent's free heading", ok, err)
	}
	expectNexusDeadline(t, core, task, record, now.Add(45*time.Minute))
	// A fresh detail page can extend a previously verified individual deadline.
	host.now = host.now.Add(16 * time.Minute)
	host.page = strings.Replace(host.page, "10:40:00", "11:10:00", 1)
	host.page = strings.Replace(host.page, "45分钟", "59分钟", 1)
	ok, err = core.refreshPromotion(context.Background(), task, record, task.N("timezone_offset"))
	if err != nil || !ok {
		t.Fatalf("renewed deadline was not accepted: verified=%t err=%v", ok, err)
	}
	expectNexusDeadline(t, core, task, record, now.Add(75*time.Minute))
}

func TestNexusDetailPromotionHDHomeGlobalWindow(t *testing.T) {
	// The NAS clock is in a different timezone from the one printed by the site.
	nasTime := time.FixedZone("UTC-7", -7*3600)
	now := time.Date(2026, 11, 24, 18, 55, 0, 0, nasTime)
	core, host, task, record := nexusDetailFixture(t, "hdhome.org", now)
	record["freedate"] = "2026-11-23T05:48:32+08:00"
	record["promotion_ended_at"] = now.Add(-24 * time.Hour).Unix()
	record["promotion_probe_at"] = record["promotion_ended_at"]
	host.page = nexusDetailHTML("157", nexusFreeTitle, nexusGlobalFree("2026-11-24 18:10:00", "2026-12-01 18:00:00"))
	ok, err := core.verifyCandidate(context.Background(), task, record)
	if err != nil || !ok || truth(record["promotion_ended_at"]) {
		t.Fatalf("current site-wide free event was not verified: verified=%t err=%v", ok, err)
	}
	expectNexusDeadline(t, core, task, record, time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC))
}

func TestNexusDetailPromotionRejectsUnrelatedEvidence(t *testing.T) {
	now := time.Date(2026, 11, 25, 9, 55, 0, 0, time.FixedZone("UTC+8", 8*3600))
	clock := nexusCountdown("2026-11-25 10:40:00", "45分钟")
	tests := []struct{ name, domain, html string }{
		{"other_versions_box_and_approval", "ubits.club", nexusDetailHTML("157", clock+nexusApproval, "")},
		{"wrong_download_id", "ubits.club", nexusDetailHTML("158", nexusFreeTitle+clock, "")},
		{"future_global_event", "hdhome.org", nexusDetailHTML("157", nexusFreeTitle, nexusGlobalFree("2026-11-26 18:10:00", "2026-12-01 18:00:00"))},
		{"login", "ubits.club", `<html><title>登录</title><form action="/takelogin.php"><input type="password"></form></html>`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, host, task, record := nexusDetailFixture(t, tc.domain, now)
			host.page = tc.html
			ok, err := core.verifyCandidate(context.Background(), task, record)
			if err != nil || ok || truth(record["promotion_verified_at"]) || host.listReads != 0 {
				t.Fatalf("unrelated evidence authorized a download: verified=%t record=%v ordinary-list reads=%d err=%v", ok, record["promotion_verified_at"], host.listReads, err)
			}
		})
	}
}

func TestNexusDetailPromotionOrdinaryList(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 12, 0, 0, time.FixedZone("UTC+8", 8*3600))
	const ordinary = `<option selected value="1">普通</option>`
	const other = `<option selected value="2">免费</option><option value="1">普通</option>`
	tests := []struct {
		name, options, resultID string
		ended                   bool
	}{
		{"matched_selected_ordinary", ordinary, "157", true},
		{"ordinary_not_selected", other, "157", false},
		{"wrong_id_in_results", ordinary, "158", false},
		{"different_option_text", `<option value="1" selected>全部</option>`, "157", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, host, task, record := nexusDetailFixture(t, "ubits.club", now)
			host.page = nexusDetailHTML("157", nexusApproval, "")
			host.listing = `<select name="spstate">` + tc.options + `</select>` +
				`<aside><a href="details.php?id=157">Footnote</a></aside>` +
				`<table class="torrents"><tr><td><a href="details.php?id=` + tc.resultID + `">Test Film</a></td></tr></table>`
			known, err := core.refreshPromotion(context.Background(), task, record, task.N("timezone_offset"))
			if err != nil || host.listReads != 1 || host.listFilter != "1" || known != tc.ended || truth(record["promotion_ended_at"]) != tc.ended ||
				truth(record["promotion_verified_at"]) {
				t.Fatalf("known=%v ended=%v listing reads=%d err=%v", known, record["promotion_ended_at"], host.listReads, err)
			}
		})
	}
}

func TestNexusDetailPromotionFailedProbeKeepsStaleRecord(t *testing.T) {
	now := time.Date(2026, 11, 25, 9, 55, 0, 0, time.FixedZone("UTC+8", 8*3600))
	core, host, task, record := nexusDetailFixture(t, "hdhome.org", now)
	old := now.Add(-24 * time.Hour)
	record["freedate"] = iso(old)
	record["promotion_probe_at"] = old.Unix()
	record["promotion_ended_at"] = old.Unix()
	record["promotion_verified_at"] = old.Unix()
	host.detailErr = errors.New("site temporarily unavailable")
	for _, elapsed := range []time.Duration{0, time.Minute} {
		host.now = now.Add(elapsed)
		ended, reason, err := core.promotionExpired(context.Background(), task, Row{"progress": 0.5}, record)
		if err != nil || ended || core.promotionSafe(task, record) {
			t.Fatalf("after %v stale evidence allowed a delete or download: ended=%v reason=%q err=%v", elapsed, ended, reason, err)
		}
	}
}

func TestNexusDetailPromotionFreeHeadingSurvivesOldCountdown(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 3, 16, 0, time.FixedZone("UTC+8", 8*3600))
	const doubleFree = `<b>[<font class="twoupfree">2X免费</font>]</b>`
	tests := []struct {
		name, title, wantFilter, selected, resultID string
		valid                                       bool
		listErr                                     error
	}{
		{"free", nexusFreeTitle, "2", `<option value="2" selected>免费</option>`, "157", true, nil},
		{"double_free", doubleFree, "4", `<option value="4" selected>2X免费</option>`, "157", true, nil},
		{"wrong_id", nexusFreeTitle, "2", `<option value="2" selected>免费</option>`, "158", false, nil},
		{"wrong_filter", nexusFreeTitle, "2", `<option value="1" selected>普通</option>`, "157", false, nil},
		{"wrong_label", doubleFree, "4", `<option value="4" selected>免费</option>`, "157", false, nil},
		{"list_unavailable", nexusFreeTitle, "2", "", "", false, errors.New("site request failed")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, host, task, record := nexusDetailFixture(t, "ubits.club", now)
			record["freedate"] = "2026-11-25T10:02:55+08:00"
			record["freedate_diff"] = "1小时"
			record["promotion_verified_at"] = now.Add(-time.Hour).Unix()
			record["add_on"] = now.Add(-48 * time.Hour).Unix()
			if tc.valid {
				task.Row["promo_max_hours"] = 1
			}
			host.page = nexusDetailHTML("157", tc.title+nexusApproval, "")
			host.listing = `<select name="spstate">` + tc.selected + `</select><table class="torrents"><tr><td><a href="details.php?id=` + tc.resultID + `">Test Film</a></td></tr></table>`
			host.listErr = tc.listErr
			ended, reason, err := core.promotionExpired(context.Background(), task, Row{"progress": 0.5}, record)
			if err != nil || ended || host.listReads != 1 || host.listFilter != tc.wantFilter {
				t.Fatalf("ended=%v reason=%q filtered reads=%d filter=%q err=%v", ended, reason, host.listReads, host.listFilter, err)
			}
			if got := truth(record["promotion_free_without_deadline"]); got != tc.valid {
				t.Fatalf("free with no deadline=%v, want %v", got, tc.valid)
			}
			if tc.valid {
				if expiry, _ := core.resolveExpiry(task, record); expiry != nil || number(record["promotion_verified_at"]) != float64(now.Unix()) {
					t.Fatalf("expected fresh evidence with no deadline, got expiry=%v verified=%v", expiry, record["promotion_verified_at"])
				}
			}
		})
	}
}
