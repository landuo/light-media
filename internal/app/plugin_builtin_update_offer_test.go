package app

import (
	"context"
	"testing"
)

func TestPluginBuiltinUpdateOffersOnlySafeCompiledTransition(t *testing.T) {
	p, request := updateCategoryFixture(t)
	ctx := context.Background()
	in := lifecycleInstallation(t, p, "CategoryEditor")
	offer, reason, err := p.builtinUpdateOffer(ctx, in)
	if err != nil || reason != "" || offer == nil || *offer != request {
		t.Fatal("safe compiled offer missing", offer, err)
	}
	if _, err = p.SaveConfig(ctx, in.InstanceID, request.Revision, map[string]any{"enabled": true, "content": "owned"}); err != nil {
		t.Fatal(err)
	}
	offer, reason, err = p.builtinUpdateOffer(ctx, in)
	if err != nil || offer != nil || reason == "" {
		t.Fatal("unsafe one-shot was offered", offer, err)
	}
	in.PluginID = "UnsupportedNative"
	if offer, reason, err = p.builtinUpdateOffer(ctx, in); err != nil || offer != nil {
		t.Fatal("unimplemented conversion offered", offer, err)
	}
}
