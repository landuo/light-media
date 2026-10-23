package app

import (
	"reflect"
	"testing"
)

func TestCatalogExplorePreferencesPersistDeduplicateAndComplete(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := t.Context()
	defaults := []string{"themoviedb", "douban", "bangumi", "anilist"}
	got, err := a.catalogExplorePreferences(ctx)
	if err != nil || !reflect.DeepEqual(got.TabOrder, defaults) {
		t.Fatalf("defaults=%+v err=%v", got, err)
	}
	want := []string{"bangumi", "douban", "themoviedb", "anilist"}
	got, err = a.saveCatalogExplorePreferences(ctx, ExplorePreferences{TabOrder: []string{"bangumi", "douban", "bangumi"}})
	if err != nil || !reflect.DeepEqual(got.TabOrder, want) {
		t.Fatalf("saved=%+v err=%v", got, err)
	}
	// A new App facade reads persisted state, not an in-memory order.
	reader := &App{Store: a.Store}
	got, err = reader.catalogExplorePreferences(ctx)
	if err != nil || !reflect.DeepEqual(got.TabOrder, want) {
		t.Fatalf("persisted=%+v err=%v", got, err)
	}
	if _, err = a.saveCatalogExplorePreferences(ctx, ExplorePreferences{TabOrder: []string{"unknown"}}); err == nil {
		t.Fatal("unknown source accepted")
	}
	got, err = reader.catalogExplorePreferences(ctx)
	if err != nil || !reflect.DeepEqual(got.TabOrder, want) {
		t.Fatal("invalid update replaced preference")
	}
	got, err = a.saveCatalogExplorePreferences(ctx, ExplorePreferences{})
	if err != nil || !reflect.DeepEqual(got.TabOrder, defaults) {
		t.Fatal("empty order did not reset defaults")
	}
}
