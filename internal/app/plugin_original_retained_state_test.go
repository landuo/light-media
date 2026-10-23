package app

import (
	"context"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	transfer "light-media/internal/plugins/torrenttransfer"
	"reflect"
	"testing"
)

// Exercise the exact constructors called by the real native configure paths.
// Direct init retains members; a newly constructed start has class defaults.
func TestPluginOriginalDirectEmptyInitRetainsNativeMembers(t *testing.T) {
	t.Run("Transfer", func(t *testing.T) {
		cfg := transfer.NewConfig()
		if _, err := cfg.Init(context.Background(), originalTransferServiceConfig(), &originalTransferInitHost{}); err != nil {
			t.Fatal(err)
		}
		before, err := cfg.Services("UTC")
		if err != nil || !cfg.State() {
			t.Fatal("fixture", err)
		}
		ctx := context.WithValue(context.Background(), originalInitializingObjectKey{}, &originalInitializationCall{nativeBefore: cfg})
		same := originalTransferConfigurationForInit(ctx)
		if _, err = same.Init(ctx, map[string]any{}, &originalTransferInitHost{}); err != nil {
			t.Fatal(err)
		}
		after, err := same.Services("UTC")
		if err != nil || !same.State() || !reflect.DeepEqual(before, after) {
			t.Fatal("empty direct init reset original members", err)
		}
		fresh := originalTransferConfigurationForInit(context.Background())
		if fresh.State() {
			t.Fatal("fresh start inherited prior object")
		}
	})
	t.Run("IYUU", func(t *testing.T) {
		cfg := iyuu.NewConfig()
		if _, err := cfg.Init(originalIYUUServiceConfig(), []any{1}); err != nil {
			t.Fatal(err)
		}
		before, err := cfg.Services("UTC")
		if err != nil || !cfg.State() {
			t.Fatal("fixture", err)
		}
		ctx := context.WithValue(context.Background(), originalInitializingObjectKey{}, &originalInitializationCall{nativeBefore: cfg})
		same := originalIYUUConfigurationForInit(ctx)
		if _, err = same.InitContext(ctx, map[string]any{}, []any{1}); err != nil {
			t.Fatal(err)
		}
		after, err := same.Services("UTC")
		if err != nil || !same.State() || !reflect.DeepEqual(before, after) {
			t.Fatal("empty direct init reset original members", err)
		}
		fresh := originalIYUUConfigurationForInit(context.Background())
		if fresh.State() {
			t.Fatal("fresh start inherited prior object")
		}
	})
}
