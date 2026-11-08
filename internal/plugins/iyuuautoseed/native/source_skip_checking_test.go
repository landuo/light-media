package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestSourceAddSkipCheckingMatchesFixedVendorMultipart(t *testing.T) {
	var oracle struct {
		Cases []struct {
			InputType  string   `json:"input_type"`
			WireValues []string `json:"wire_values"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-source-add-wire-20261105/source-wire.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 33 {
		t.Fatal("fixed independent installed-vendor wire33 corpus missing", err)
	}
	for _, route := range []string{"paused", "unpaused", "limits", "url_material"} {
		t.Run(route, func(t *testing.T) {
			for i, sample := range oracle.Cases {
				t.Run(fmt.Sprintf("%02d-%s", i, sample.InputType), func(t *testing.T) {
					var writes atomic.Int32
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/api/v2/app/webapiVersion" {
							io.WriteString(w, "2.11.4")
							return
						}
						if r.URL.Path != "/api/v2/torrents/add" {
							t.Error("unexpected native route", r.URL.Path)
							http.Error(w, "unexpected", 400)
							return
						}
						writes.Add(1)
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						actual := r.MultipartForm.Value["skip_checking"]
						if !reflect.DeepEqual(actual, sample.WireValues) && !(len(actual) == 0 && len(sample.WireValues) == 0) {
							t.Errorf("source wire mismatch: got%q want%q", actual, sample.WireValues)
						}
						if len(actual) > 1 {
							t.Error("duplicate skip_checking field", actual)
						}
						io.WriteString(w, `{"added_torrent_ids":{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{}},"errors":{}}`)
					}))
					defer peer.Close()
					c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					spec := IYUUAddSpec{Torrent: []byte("owned material"), IgnoreCategoryCheck: true}
					if len(sample.WireValues) == 1 {
						value := sample.WireValues[0]
						spec.SkipCheckingWire = &value
					}
					var result IYUUAddResult
					switch route {
					case "paused":
						result, err = c.PluginHostAdd(context.Background(), spec, true, nil, nil)
					case "unpaused":
						result, err = c.PluginHostAdd(context.Background(), spec, false, nil, nil)
					case "limits":
						limit := int64(1024)
						result, err = c.PluginHostAdd(context.Background(), spec, true, &limit, nil)
					case "url_material":
						spec.Torrent = nil
						address := "https://owned.invalid/material"
						result, err = c.PluginHostAddMaterial(context.Background(), spec, true, nil, nil, &address, nil)
					}
					if err != nil || !result.Accepted || result.Outcome != "confirmed" || writes.Load() != 1 {
						t.Fatal("source wire altered selected mutation outcome", result, err, writes.Load())
					}
				})
			}
		})
	}
}
func TestOrdinarySkipCheckingBoolKeepsExistingMultipartBytes(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/app/webapiVersion" {
					io.WriteString(w, "2.11.4")
					return
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				if got := r.MultipartForm.Value["skip_checking"]; !reflect.DeepEqual(got, []string{iyuuBool(flag)}) {
					t.Error("ordinary bool field bytes changed", got)
				}
				io.WriteString(w, "Ok.")
			}))
			defer peer.Close()
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			out, err := c.PluginHostAdd(context.Background(), IYUUAddSpec{Torrent: []byte("owned"), SkipChecking: &flag}, true, nil, nil)
			if err != nil || !out.Accepted {
				t.Fatal(out, err)
			}
		})
	}
}
