package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transfer "light-media/internal/plugins/torrenttransfer"
)

// Only synthetic stopped-host observations are used in owned encrypted stores.
// This never stops or observes the user's original container.
func ownedStoppedPluginSourceObservation() stoppedPluginSource {
	return stoppedPluginSource{ContainerID: originalPluginContainerID, ImageSHA256: tokenHash("owned-test-source-image"), FinishedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), ObservedAt: now(), RestartPolicy: "no", PID: 0}
}
func ownedStoppedPluginSourceHandoff(t *testing.T, p *PluginStore, instance string) {
	t.Helper()
	stored, e := p.Config(context.Background(), instance)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.SaveConfigAndQueue(context.Background(), instance, stored.Revision, stored.Value); !pluginHandoffPending(e) {
		t.Fatal("reviewed source became executable without an execution handoff", e)
	}
	actual, e := p.Config(context.Background(), instance)
	if e != nil || actual.Revision != stored.Revision || !bytes.Equal(actual.Value, stored.Value) {
		t.Fatal("blocked handoff changed source configuration", e)
	}
	if e := p.ImportReviewedPluginExecutionHandoff(context.Background(), instance, reviewedIYUUJSON(ownedStoppedPluginSourceObservation())); e != nil {
		t.Fatal("owned stopped-source handoff rejected", e)
	}
}
func TestPluginSourceHandoffRejectsDataImportedBeforeStop(t *testing.T) {
	stopped := ownedStoppedPluginSourceObservation()
	source := pluginExecutionSource{ImportedAt: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)}
	if e := pluginHandoffSnapshotTime(source, stopped); pluginErrorCodeInternal(e) != "PLUGIN_HANDOFF_STALE_SNAPSHOT" {
		t.Fatal("pre-stop import was treated as final data", e)
	}
	source.ImportedAt = now()
	if e := pluginHandoffSnapshotTime(source, stopped); e != nil {
		t.Fatal(e)
	}
}
func TestPluginSourceHandoffRejectsMissingAndRunningObservations(t *testing.T) {
	_, p := pluginStoreFixture(t)
	for _, shape := range []string{"{}", "null", `{"running":false}`} {
		if e := p.ImportReviewedPluginExecutionHandoff(context.Background(), "TorrentTransfer", []byte(shape)); pluginErrorCodeInternal(e) != "PLUGIN_HANDOFF_INVALID" {
			t.Fatal("incomplete source observation accepted", e)
		}
	}
	for _, mutate := range []func(*stoppedPluginSource){
		func(v *stoppedPluginSource) { v.Running = true },
		func(v *stoppedPluginSource) { v.Restarting = true },
		func(v *stoppedPluginSource) { v.PID = 53 },
		func(v *stoppedPluginSource) { v.ContainerID = strings.Repeat("f", 64) },
		func(v *stoppedPluginSource) { v.RestartPolicy = "always" },
		func(v *stoppedPluginSource) { v.RestartPolicy = "unless-stopped" },
		func(v *stoppedPluginSource) { v.ImageSHA256 = "unknown" },
		func(v *stoppedPluginSource) { v.FinishedAt = "" },
		func(v *stoppedPluginSource) {
			v.ObservedAt = time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano)
		},
		func(v *stoppedPluginSource) {
			v.ObservedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
		},
	} {
		v := ownedStoppedPluginSourceObservation()
		mutate(&v)
		if e := p.ImportReviewedPluginExecutionHandoff(context.Background(), "TorrentTransfer", reviewedIYUUJSON(v)); pluginErrorCodeInternal(e) != "PLUGIN_HANDOFF_INVALID" {
			t.Fatal("live, stale or unrelated source observation accepted", e)
		}
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(reviewedIYUUJSON(ownedStoppedPluginSourceObservation()), &fields)
	for _, key := range []string{"running", "restarting", "pid"} {
		original := fields[key]
		delete(fields, key)
		if e := p.ImportReviewedPluginExecutionHandoff(context.Background(), "TorrentTransfer", reviewedIYUUJSON(fields)); pluginErrorCodeInternal(e) != "PLUGIN_HANDOFF_INVALID" {
			t.Fatal("absent state treated as stopped", e)
		}
		fields[key] = json.RawMessage(`null`)
		if e := p.ImportReviewedPluginExecutionHandoff(context.Background(), "TorrentTransfer", reviewedIYUUJSON(fields)); pluginErrorCodeInternal(e) != "PLUGIN_HANDOFF_INVALID" {
			t.Fatal("unknown state treated as stopped", e)
		}
		fields[key] = original
	}
}

func reviewedTransferHandoffAssertions(t *testing.T, p *PluginStore, source reviewedTransferSnapshot, raw []byte) {
	t.Helper()
	ctx := context.Background()
	stored, e := p.Config(ctx, source.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.SaveConfigAndQueue(ctx, source.PluginID, stored.Revision, json.RawMessage(source.Config)); !pluginHandoffPending(e) {
		t.Fatal("reviewed names alone activated source before execution handoff", e)
	}
	in, e := p.Installation(ctx, source.PluginID)
	if e != nil || in.DesiredEnabled {
		t.Fatal("blocked save activated imported source", e)
	}
	if _, e = p.EnqueueRun(ctx, source.PluginID, "run", nil); e == nil {
		t.Fatal("staged source enqueued work")
	}
	ownedStoppedPluginSourceHandoff(t, p, source.PluginID)
	if e = p.requirePluginExecutionHandoff(ctx, source.PluginID); e != nil {
		t.Fatal(e)
	}
	_, run, e := p.SaveConfigAndQueue(ctx, source.PluginID, stored.Revision, json.RawMessage(source.Config))
	if e != nil {
		t.Fatal("delivered source could not initialize", e)
	}
	if _, e = p.store.DB.Exec(`DELETE FROM records WHERE kind=? AND id=?`, pluginExecutionHandoffKind, source.PluginID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.claimRunForInstance(ctx, source.PluginID); e != sql.ErrNoRows {
		t.Fatal("queued source crossed removed handoff", e)
	}
	other, e := p.InstallAndQueue(ctx, PluginInstallation{InstanceID: "OwnedOther", PluginID: source.PluginID, Version: "1.10.3", ManifestSchemaVersion: 1, Manifest: reviewedIYUUJSON(transfer.Manifest(source.PluginID))}, json.RawMessage(`{"enabled":false}`))
	if e != nil {
		t.Fatal(e)
	}
	claimedOther, e := p.ClaimRun(ctx)
	if e != nil || claimedOther.ID != other.ID {
		t.Fatal("waiting original handoff starved a fresh native instance", e)
	}
	actual, e := p.Run(ctx, run.ID)
	if e != nil || actual.State != "pending" {
		t.Fatal("missing handoff consumed original pending identity", e)
	}
	ownedStoppedPluginSourceHandoff(t, p, source.PluginID)
	actual, e = p.claimRunForInstance(ctx, source.PluginID)
	if e != nil || actual.ID != run.ID || actual.State != "running" {
		t.Fatal("handoff did not release the same queued identity", e)
	}
	if _, e = p.store.DB.Exec(`DELETE FROM records WHERE kind=? AND id=?`, pluginExecutionHandoffKind, source.PluginID); e != nil {
		t.Fatal(e)
	}
	if e = p.requirePluginExecutionHandoff(ctx, source.PluginID); !pluginHandoffPending(e) {
		t.Fatal("source execution did not recheck removed handoff", e)
	}
	in, e = p.Installation(ctx, source.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	a := &App{Store: p.store, pluginStore: p}
	if e = a.validatePluginHostRun(ctx, in, &actual, transfer.ExecuteCapability); !pluginHandoffPending(e) {
		t.Fatal("claimed native host IO bypassed removed handoff", e)
	}
	if e = p.ImportReviewedPluginExecutionHandoff(ctx, source.PluginID, reviewedIYUUJSON(ownedStoppedPluginSourceObservation())); !pluginHandoffPending(e) {
		t.Fatal("offline handoff overwrote a running source timeline", e)
	}
}

func reviewedTransferHandoffCLI(t *testing.T, source reviewedTransferSnapshot, raw []byte) {
	t.Helper()
	binary := os.Getenv("LIGHT_MEDIA_HANDOFF_CLI")
	if !filepath.IsAbs(binary) {
		t.Fatal("owned handoff CLI required")
	}
	s, p, _ := reviewedTransferOwned(t, source)
	m := reviewedTransferRun(t, p, raw, source.PluginID)
	if _, e := p.ImportReviewedTorrentTransfer(context.Background(), m.ID, raw); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Dir(s.lock.Name())
	input := reviewedIYUUJSON(ownedStoppedPluginSourceObservation())
	invoke := func(want bool, extra ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(binary, append([]string{"--instance-id", source.PluginID}, extra...)...)
		cmd.Stdin = bytes.NewReader(input)
		out, e := cmd.CombinedOutput()
		if (e == nil) != want {
			t.Fatal("handoff CLI returned wrong outcome", e)
		}
		if !want {
			return nil
		}
		var value map[string]any
		if json.Unmarshal(out, &value) != nil {
			t.Fatal("handoff CLI response not bounded JSON")
		}
		return value
	}
	invoke(false, "--target-data-dir", dir, "--apply")
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	before := reviewedIYUUFileSnapshot(t, dir)
	if v := invoke(true, "--target-data-dir", dir); v["state"] != "ready" || v["writes"] != float64(0) {
		t.Fatal("handoff CLI read-only check failed")
	}
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("handoff CLI read-only check changed bytes or modes")
	}
	if v := invoke(true, "--target-data-dir", dir, "--apply"); v["state"] != "accepted" || v["executed"] != false || v["source_mutations"] != float64(0) {
		t.Fatal("handoff CLI activated work or wrote source")
	}
	cold, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	p, e = NewPluginStore(context.Background(), cold)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.requirePluginExecutionHandoff(context.Background(), source.PluginID); e != nil {
		t.Fatal("CLI handoff did not survive cold read", e)
	}
	in, e := p.Installation(context.Background(), source.PluginID)
	if e != nil || in.DesiredEnabled {
		t.Fatal("CLI automatically activated source", e)
	}
}
