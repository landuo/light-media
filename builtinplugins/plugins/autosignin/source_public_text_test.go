package autosignin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	sdk "light-media/builtinplugins/internal/sdk"
	"os"
	"testing"
)

func TestTjuptPublicTextActualSourceHeaders(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-oracles/source-public-content-type-20261106/source.json")
	if e != nil {
		t.Fatal(e)
	}
	var src struct {
		Rows []struct {
			Name        string
			ContentType string `json:"content_type"`
			BodyHex     string `json:"body_hex"`
			Text        string
		}
	}
	if e = json.Unmarshal(raw, &src); e != nil {
		t.Fatal(e)
	}
	if len(src.Rows) != 18 {
		t.Fatal("actual source cases")
	}
	for _, r := range src.Rows {
		t.Run(r.Name, func(t *testing.T) {
			body, e := hex.DecodeString(r.BodyHex)
			if e != nil {
				t.Fatal(e)
			}
			host := func(ctx context.Context, method string, in json.RawMessage) (json.RawMessage, error) {
				if method != sdk.NetworkGetCapability {
					t.Fatal(method)
				}
				var q sdk.NetworkGetRequestV1
				if e := json.Unmarshal(in, &q); e != nil {
					t.Fatal(e)
				}
				if q.URL != "https://movie.douban.com/j/subject_suggest?q=原样&literal#片名" || q.Profile != sdk.NetworkGetSourcePublicProfile {
					t.Fatal("original raw f-string changed", q)
				}
				return json.Marshal(sdk.NetworkGetResultV1{SchemaVersion: 1, Response: &sdk.NetworkResponseV1{StatusCode: 200, Content: body, ContentType: r.ContentType}})
			}
			pub := sdkTjuptPublic{backend: sdkBackend{ctx: context.Background(), host: host}}
			page, e := pub.Suggest(context.Background(), "原样&literal#片名")
			if e != nil || page == nil {
				t.Fatal(e)
			}
			text, e := page.getText()
			if e != nil || text != r.Text {
				t.Fatalf("text got%q/%v source%q", text, e, r.Text)
			}
		})
	}
}
