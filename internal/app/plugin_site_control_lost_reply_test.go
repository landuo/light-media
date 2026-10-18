package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func TestSiteControlLostReplyWorkerFixture(t *testing.T) {
	for _, arg := range os.Args {
		if arg != "--owned-site-commit-reply-loss" {
			continue
		}
		manifest, handle, ok := builtin.Lookup("MergeSiteSwitch")
		if !ok {
			os.Exit(9)
		}
		_ = plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
			out, e := handle(ctx, method, raw, host)
			if e == nil && method == "invoke" {
				os.Exit(7)
			} // after the actual native transaction, before SDK result
			return out, e
		})
		os.Exit(0)
	}
}

func TestPluginSiteSwitchCommittedSaveSurvivesActualLostSDKReply(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{identityRequire(t, a, "s2").SourceID}, "search_follow_enable_sites": true})
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	a.pluginCommand = func(string) (plugins.Command, error) {
		return plugins.Command{Path: exe, Args: []string{"-test.run=^TestSiteControlLostReplyWorkerFixture$", "--", "--owned-site-commit-reply-loss"}}, nil
	}
	run, e := a.pluginStore.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	a.executePluginRun(ctx, run)
	got, e := a.pluginStore.Run(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.siteControlReceipt(ctx, got); e != nil {
		t.Fatal("actual native save did not commit", e)
	}
	if got.State != "succeeded" || got.ErrorCode != "" || !a.Settings().Sites[1].Enabled || !bytes.Equal(got.Input, run.Input) || got.ConfigRevision != run.ConfigRevision {
		t.Fatalf("committed native operation reported %s/%s after actual SDK reply loss", got.State, got.ErrorCode)
	}
}

func TestPluginSiteSwitchColdFailedCommittedReceiptRecoversOriginalIdentity(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{identityRequire(t, a, "s2").SourceID}})
	run := siteSwitchExecute(t, a, true)
	if _, e := a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed',result=NULL,error_code='worker_exited' WHERE id=?`, run.ID); e != nil {
		t.Fatal(e)
	}
	site := a.Settings().Sites[1]
	site.Enabled = false
	if _, e := a.saveManagedSite(ctx, site); e != nil {
		t.Fatal(e)
	}
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	got, e := b.pluginStore.Run(ctx, run.ID)
	if e != nil || got.State != "succeeded" || got.ErrorCode != "" || !bytes.Equal(run.Input, got.Input) || run.ConfigRevision != got.ConfigRevision || b.Settings().Sites[1].Enabled {
		t.Fatal("old failed but committed source save not recovered without replay", got.State, e)
	}
}

func TestPluginSiteSwitchColdFailedWithoutReceiptDoesNotInventSuccess(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []builtin.SiteReference{{Custom: "s2"}}})
	failed := siteSwitchExecute(t, a, false)
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	old, e := b.pluginStore.Run(ctx, failed.ID)
	if e != nil || old.State != "failed" || old.ErrorCode != failed.ErrorCode || !bytes.Equal(old.Input, failed.Input) || b.Settings().Sites[1].Enabled {
		t.Fatal("uncommitted failure fabricated success or replayed", old.State, e)
	}
}

func TestPluginSiteSwitchColdFailedCorruptCommittedReceiptRefusesRecovery(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{identityRequire(t, a, "s2").SourceID}})
	run := siteSwitchExecute(t, a, true)
	var sealed []byte
	if e := a.Store.DB.QueryRow(`SELECT result FROM plugin_site_control_operations WHERE run_id=?`, run.ID).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	sealed[len(sealed)-1] ^= 1
	if _, e := a.Store.DB.Exec(`UPDATE plugin_site_control_operations SET result=? WHERE run_id=?`, sealed, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed',result=NULL,error_code='worker_exited' WHERE id=?`, run.ID); e != nil {
		t.Fatal(e)
	}
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e == nil {
		b.Close()
		t.Fatal("corrupt committed receipt treated as successful or uncommitted")
	}
}
