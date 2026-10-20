package app

import (
	"context"
	bf "light-media/internal/plugins/brushflow"
	"testing"
)

func TestBrushFlowSourceSizedSchedulesCoexistWithOtherActualSDK(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cfg := brushCapacityConfig(t, a, 5001)
	in, initial := brushInstall(t, a, bf.DefaultConfig())
	brushFinish(t, a, initial)
	stored, e := a.pluginStore.Config(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, in.InstanceID, stored.Revision, cfg); e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	brushFinish(t, a, run)
	var brush int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, in.InstanceID).Scan(&brush); e != nil || brush != 10002 {
		t.Fatal("source Brush set", e, brush)
	}
	// Both installation and cron registration execute the other native plugin's
	// actual SDK path. No backup service is fired and no external write occurs.
	cookie := autoTestInstall(t, a)
	autoTestConfig(t, a, cookie, map[string]any{"enabled": true, "cron": "*/15 * * * *", "onlyonce": false})
	var other int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoBackup' AND active=1`).Scan(&other); e != nil || other != 1 {
		t.Fatal("other actual SDK timer blocked", e, other)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	exempt, e := a.pluginStore.brushFlowExemptScheduleCountTx(ctx, tx)
	tx.Rollback()
	if e != nil || exempt != 10002 {
		t.Fatal("authenticated exemption", e, exempt)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_schedules SET definition_digest='forged' WHERE instance_id=? AND service_id='Task_capacity00000000_Brush'`, in.InstanceID); e != nil {
		t.Fatal(e)
	}
	tx, e = a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.pluginStore.brushFlowExemptScheduleCountTx(ctx, tx)
	tx.Rollback()
	if e == nil {
		t.Fatal("plaintext row claimed native exemption without exact authenticated definition")
	}
}
