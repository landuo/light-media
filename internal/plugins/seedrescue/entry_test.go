package seedrescue

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNativeEntryDeclaresOriginalFederatedConfigAndPage(t *testing.T) {
	for method, export := range map[string]string{"config_form": "./Config", "page": "./Page"} {
		raw, e := NativeHandle(context.Background(), method, nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		var result Object
		if json.Unmarshal(raw, &result) != nil || result["render_mode"] != "vue" || result["path"] != "dist/assets" || result["entry"] != "remoteEntry.js" || result["export"] != export {
			t.Fatal(method, string(raw))
		}
	}
}
