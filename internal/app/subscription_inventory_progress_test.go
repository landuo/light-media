package app

import (
	"light-media/internal/catalog"
	"testing"
)

func TestInventoryProgressReconcilesCommittedSeasonAndCheck(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := t.Context()
	lack := 50
	sub := Subscription{ID: "progress", Media: catalog.Media{ID: 101172, MediaType: "tv"}, Season: 1, StartEpisode: 248, TotalEpisodes: 300, SourceNote: []int{248, 249}, LackEpisode: &lack, Revision: 3, State: "active"}
	if err := a.Store.Put(ctx, "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}
	previous, err := a.loadSubscriptionDisplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, ep := range []int{247, 248, 249, 250, 251, 301} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO asset_slots VALUES(?,?,?,?)", mediaKey(sub.Media), 1, ep, "video"); err != nil {
			t.Fatal(err)
		}
	}
	if err = reconcileInventorySubscriptions(ctx, tx, &sub.Media); err != nil {
		t.Fatal(err)
	}
	if v, err := a.loadSubscriptionDisplay(ctx); err != nil || v != previous {
		t.Fatal("uncommitted inventory visible", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var stored, native, revision int
		if err := a.Store.reader().QueryRowContext(ctx, "SELECT lack_episode,json_extract(payload,'$.lack_episode'),json_extract(payload,'$.revision') FROM subscribe WHERE uid=?", sub.ID).Scan(&native, &stored, &revision); err != nil || native != 49 || stored != 49 || revision != 3 {
			t.Fatalf("persisted %d/%d, revision %d, err %v", native, stored, revision, err)
		}
		if i == 0 {
			if err := a.recordSubscriptionCheckLack(ctx, sub, "older coverage", nil, 51); err != nil {
				t.Fatal(err)
			}
		}
	}
	current, err := a.loadSubscriptionDisplay(ctx)
	if err != nil || current == previous || !current.coverage[subscriptionDisplayKey(sub)].library[251] {
		t.Fatal("committed inventory missing", err)
	}
	sub.SourceNote = []int{249, 250} // Accepted notes do not prove a library file.
	owned := map[int]bool{248: true, 251: true}
	sub.LackEpisode = nil
	if n, ok := inventorySubscriptionLack(sub, owned); !ok || n != 51 {
		t.Fatalf("gap incorrectly counted %v,%v", n, ok)
	}
	sub.BestVersion = true
	if _, ok := inventorySubscriptionLack(sub, owned); ok {
		t.Fatal("quality progress overwritten")
	}
}
