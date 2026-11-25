package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

type downloaderReplayHost struct {
	Host
	adapter sourceDownloaderReads
}

func (h downloaderReplayHost) Inventory(ctx context.Context, mode string, selection any) ([]Service, error) {
	return h.adapter.Inventory(ctx, mode, selection)
}

func (h downloaderReplayHost) AddPaused(ctx context.Context, s Service, spec AddSpec) (AddReceipt, error) {
	return h.adapter.AddPaused(ctx, s, spec)
}

func (h downloaderReplayHost) FindByTag(ctx context.Context, s Service, tag string) (string, error) {
	return h.adapter.FindByTag(ctx, s, tag)
}

func (h downloaderReplayHost) SetCategory(ctx context.Context, s Service, hash string, value any) (EffectReceipt, error) {
	return h.adapter.SetCategory(ctx, s, hash, value)
}

func (h downloaderReplayHost) RandomTag(context.Context, int) (string, error) {
	return "TestMarker", nil
}

func TestSourceIYUUDownloaderReplay222(t *testing.T) {
	var oracle struct {
		Configs   []sdk.BrushDownloaderReferenceV1 `json:"configs"`
		Inactive  []string                         `json:"inactive_ids"`
		Inventory []struct {
			Name, Mode, Error string
			Selection         any
			Expected, Active  []Service
		}
		Mutations []struct {
			Name, Metadata, Runtime, Error, Result string
			Reuse, Accepted                        bool
			Save                                   any
			Skip                                   json.RawMessage
			NativeCalled                           bool    `json:"native_called"`
			WrapperTruthy                          bool    `json:"wrapper_truthy"`
			SkipWire                               *string `json:"skip_wire"`
			Followup                               [][]string
			BoundAdd                               struct {
				DownloadDir         *string  `json:"download_dir"`
				Paused              bool     `json:"is_paused"`
				Tags                []string `json:"tag"`
				Labels              []string `json:"labels"`
				Category            *string  `json:"category"`
				IgnoreCategoryCheck *bool    `json:"ignore_category_check"`
			} `json:"bound_add"`
		}
	}
	raw, err := os.ReadFile("reference/sdk-downloader-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range oracle.Inventory {
		t.Run(tc.Name, func(t *testing.T) {
			var visited []string
			host := downloaderReplayHost{adapter: sourceDownloaderReads{client: sdk.Downloaders{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.DownloaderConfigurationsReadCapability:
					return json.Marshal(sdk.DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: oracle.Configs})
				case sdk.DownloaderSourceConnectionCapability:
					req := decodeDownloaderReplay[sdk.DownloaderConnectionRequestV1](t, raw)
					visited = append(visited, req.DownloaderID)
					for _, ref := range oracle.Configs {
						if ref.ID == req.DownloaderID {
							return json.Marshal(sdk.DownloaderSourceConnectionV1{
								DownloaderConnectionV1: sdk.DownloaderConnectionV1{
									SchemaVersion: 1, DownloaderID: ref.ID, Kind: ref.Kind, Inactive: containsText(oracle.Inactive, ref.ID),
								}, Exists: true,
							})
						}
					}
				}
				t.Fatalf("unexpected SDK call %s", method)
				return nil, nil
			}}}}
			cfg := NewConfig()
			cfg.values["downloaders"] = tc.Selection
			cfg.values["auto_downloader"] = tc.Selection
			core := Core{config: cfg, host: host}
			list, dest, err := core.active(ctx, tc.Mode)
			assertDownloaderReplayFault(t, tc.Error, err)
			active := []Service{}
			if tc.Mode == "auto" && dest != nil {
				active = append(active, *dest)
			} else if tc.Mode == "services" {
				active = append(active, list...)
			}
			if !reflect.DeepEqual(active, tc.Active) {
				t.Fatalf("active wrappers: got %#v, want %#v", active, tc.Active)
			}
			var want []string
			switch tc.Name {
			case "last wrapper first name slot":
				want = []string{"newqb", "another", "trdual", "trfirst"}
			case "first inactive wrapper wins":
				want = []string{"newqb"}
			case "string filter is characters":
				want = []string{"single"}
			case "mapping filter is keys":
				want = []string{"newqb", "trdual"}
			}
			if !reflect.DeepEqual(visited, want) {
				t.Fatalf("connection lookups: got %v, want %v", visited, want)
			}
			for _, s := range tc.Expected {
				if !containsText(visited, s.Ref) {
					t.Fatalf("source wrapper %q was not consulted", s.Ref)
				}
			}
		})
	}
	for _, tc := range oracle.Mutations {
		t.Run(tc.Name, func(t *testing.T) {
			const id = "owned"
			hash := strings.Repeat("a", 40)
			var followed [][]string
			adds := 0
			host := downloaderReplayHost{adapter: sourceDownloaderReads{client: sdk.Downloaders{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.DownloaderSourceAddCapability:
					adds++
					want := sdk.DownloaderSourceAddRequestV1{
						DownloaderAddRequestV1: sdk.DownloaderAddRequestV1{
							DownloaderID: id, Torrent: []byte("owned"), Paused: tc.BoundAdd.Paused,
							SavePath: tc.BoundAdd.DownloadDir, Category: tc.BoundAdd.Category,
							IgnoreCategoryCheck: tc.BoundAdd.IgnoreCategoryCheck, Tags: tc.BoundAdd.Tags,
						}, SkipCheckingWire: tc.SkipWire,
					}
					if tc.Metadata == "transmission" {
						if tc.Runtime == "qbittorrent" {
							want.QBLabels = tc.BoundAdd.Labels
						} else {
							want.Tags = tc.BoundAdd.Labels
						}
					}
					got := decodeDownloaderReplay[sdk.DownloaderSourceAddRequestV1](t, raw)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("add request: got %s, want %+v", raw, want)
					}
					out := sdk.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: id, Accepted: tc.Accepted, Outcome: "confirmed"}
					if !tc.Accepted {
						out.Outcome = "rejected"
					} else if tc.Runtime == "transmission" {
						out.Hash = hash
					}
					return json.Marshal(sdk.DownloaderSourceMutationResultV1{DownloaderMutationV1: out, NativeError: !tc.Accepted})
				case sdk.DownloaderSourceFindByTagCapability:
					req := decodeDownloaderReplay[sdk.DownloaderFindByTagRequestV1](t, raw)
					if req.DownloaderID != id || req.Status != nil {
						t.Fatalf("marker request: %s", raw)
					}
					followed = append(followed, append([]string{"find"}, req.Tags...))
					return json.Marshal(sdk.DownloaderFindByTagV1{SchemaVersion: 1, DownloaderID: id, Hash: &hash})
				case sdk.DownloaderSourceControlCapability:
					req := decodeDownloaderReplay[sdk.DownloaderControlRequestV1](t, raw)
					if req.DownloaderID != id || req.Action != "set_category" || req.Category == nil {
						t.Fatalf("category request: %s", raw)
					}
					followed = append(followed, append(append([]string{"category"}, req.Hashes...), *req.Category))
					return json.Marshal(sdk.DownloaderSourceMutationResultV1{DownloaderMutationV1: sdk.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: id, Outcome: "confirmed", Accepted: true}})
				}
				t.Fatalf("unexpected SDK call %s", method)
				return nil, nil
			}}}}
			config := NewConfig()
			config.values["labelsafterseed"] = " A ,,B "
			config.values["addhosttotag"] = true
			config.values["reuse_save_path"] = tc.Reuse
			config.skipRaw = tc.Skip
			core := Core{config: config, host: host}
			got, err := core.download(ctx, Service{Ref: id, Name: id, Kind: tc.Metadata, RuntimeKind: tc.Runtime}, TorrentContent{Bytes: []byte("owned")}, tc.Save, "owned-category", "test-site")
			assertDownloaderReplayFault(t, tc.Error, err)
			if got != tc.Result || (adds == 1) != tc.NativeCalled || adds > 1 || !reflect.DeepEqual(append([][]string{}, followed...), tc.Followup) {
				t.Fatalf("result=%q adds=%d follow-up=%v, want %q/%v/%v", got, adds, followed, tc.Result, tc.NativeCalled, tc.Followup)
			}
		})
	}
	t.Run("unknown and post-mutation outcomes stop follow-up", func(t *testing.T) {
		for _, result := range []sdk.DownloaderMutationV1{
			{SchemaVersion: 1, DownloaderID: "owned", Outcome: "unknown"},
			{SchemaVersion: 1, DownloaderID: "owned", Outcome: "confirmed", Accepted: true, FailureCode: "CALL_CHANGED_AFTER_MUTATION"},
		} {
			calls := 0
			host := downloaderReplayHost{adapter: sourceDownloaderReads{client: sdk.Downloaders{Host: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				if method != sdk.DownloaderSourceAddCapability {
					t.Fatalf("effect fence allowed %s", method)
				}
				calls++
				return json.Marshal(sdk.DownloaderSourceMutationResultV1{DownloaderMutationV1: result, NativeError: result.Outcome == "unknown"})
			}}}}
			core := Core{config: NewConfig(), host: host}
			_, err := core.download(ctx, Service{Ref: "owned", Kind: "qbittorrent"}, TorrentContent{Bytes: []byte("owned")}, nil, nil, "")
			if err == nil || calls != 1 || len(core.attempts) != 1 || core.attempts[0].Outcome != result.Outcome {
				t.Fatalf("effect fence: err=%v calls=%d attempts=%+v", err, calls, core.attempts)
			}
		}
	})
}

func decodeDownloaderReplay[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func containsText(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func assertDownloaderReplayFault(t *testing.T, want string, err error) {
	t.Helper()
	var fault *SourceFault
	if want == "" && err != nil || want != "" && (!errors.As(err, &fault) || fault.Class != want) {
		t.Fatalf("error: got %v, want %q", err, want)
	}
}
