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

// These are captured real Requests-to-peer fields from the fixed source vendor,
// not a claim that a daemon accepts unknown labels or that TT finishes transfer.
func TestSourceAddQBLabelsMatchSourcePeerAtAllBuilders(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Labels       []string
			NetworkPOSTs int `json:"network_POSTs_to_owned_peer"`
			PeerResponse struct {
				Status int
				Body   string
			} `json:"peer_response"`
			Trace []struct {
				Event  string
				Fields []struct{ Name, Value string } `json:"non_file_fields"`
			}
		}
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-source-add-labels-20261105/source-add-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("independent actual source peer oracle", err)
	}
	for _, route := range []string{"paused", "unpaused", "limits", "url_material"} {
		t.Run(route, func(t *testing.T) {
			for i, sample := range oracle.Cases {
				if sample.NetworkPOSTs != 1 {
					continue
				} // lone-surrogate source text is outside Go UTF-8 request boundary
				t.Run(fmt.Sprint(i), func(t *testing.T) {
					expected := []string{}
					for _, step := range sample.Trace {
						if step.Event == "peer.received.POST" {
							for _, f := range step.Fields {
								if f.Name == "labels" {
									expected = append(expected, f.Value)
								}
							}
						}
					}
					if !reflect.DeepEqual(sample.Labels, expected) {
						t.Fatal("source oracle labels order invalid")
					}
					var writes atomic.Int32
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/api/v2/app/webapiVersion" {
							io.WriteString(w, "2.11.4")
							return
						}
						if r.URL.Path != "/api/v2/torrents/add" {
							t.Error("unexpected native request", r.URL.Path)
							return
						}
						writes.Add(1)
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						got := r.MultipartForm.Value["labels"]
						if len(got) != len(expected) || len(got) > 0 && !reflect.DeepEqual(got, expected) {
							t.Errorf("ordered source labels got%q want%q", got, expected)
						}
						if len(r.MultipartForm.Value["tags"]) != 0 {
							t.Error("labels were reinterpreted as tags")
						}
						w.WriteHeader(sample.PeerResponse.Status)
						io.WriteString(w, sample.PeerResponse.Body)
					}))
					defer peer.Close()
					c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					spec := IYUUAddSpec{Torrent: []byte("owned material"), IgnoreCategoryCheck: true, QBLabels: sample.Labels}
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
					accepted := sample.PeerResponse.Status == 200 && sample.PeerResponse.Body == "Ok."
					outcome := "rejected"
					if accepted {
						outcome = "confirmed"
					} else if sample.PeerResponse.Status != 200 {
						outcome = "unknown"
					}
					if writes.Load() != int32(sample.NetworkPOSTs) || result.Accepted != accepted || result.Outcome != outcome || accepted && err != nil {
						t.Fatal("labels changed selected mutation count/outcome", result, err, writes.Load())
					}
				})
			}
		})
	}
}
