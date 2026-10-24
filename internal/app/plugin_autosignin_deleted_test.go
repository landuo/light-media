package app

import (
	"encoding/json"
	source "light-media/internal/plugins/autosignin"
	"reflect"
	"testing"
	"time"
)

func TestAutoSignInDeletedOriginalNumericCoercionAndAll(t *testing.T) {
	// The installed source intentionally compares int(site), including numeric
	// custom strings. The host must not convert the trusted numeric event to text.
	engine := source.New(source.Inventory{}, time.Now)
	engine.Enabled = true
	engine.Config = source.Object{"sign_sites": []any{json.Number("1"), "1", json.Number("2")}, "login_sites": []any{"all"}}
	engine.Data = source.Object{"history": []any{"preserved"}}
	if err := engine.SiteDeleted(json.Number("1")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(engine.SignSites, []any{json.Number("2")}) || !reflect.DeepEqual(engine.LoginSites, []any{"all"}) || !reflect.DeepEqual(engine.Data, source.Object{"history": []any{"preserved"}}) {
		t.Fatal("source deletion order/coercion/history changed", engine.SignSites, engine.LoginSites, engine.Data)
	}
}
