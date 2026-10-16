package larkmessager

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"testing"
)

type moduleAlgorithmHost struct{ algorithmHost }

type responseIDHost struct{ ids []any }

func (h *responseIDHost) API(_ context.Context, r Request) (Response, error) {
	return Response{Status: 200, Body: uuidJSON(Object{"code": 0, "data": Object{"message_id": h.ids[0], "chat_id": h.ids[1], "msg_type": "interactive"}})}, nil
}
func (*responseIDHost) ReadFile(context.Context, string) (Blob, error)   { return Blob{}, ErrDenied }
func (*responseIDHost) FetchImage(context.Context, string) (Blob, error) { return Blob{}, ErrDenied }

func (h *moduleAlgorithmHost) API(ctx context.Context, r Request) (Response, error) {
	response, e := h.algorithmHost.API(ctx, r)
	if e == nil && (r.Op == "resource_get" || r.Op == "image_get" || r.Op == "file_get") {
		response.Body = []byte{}
		response.Header = map[string]string{}
	}
	return response, e
}
func mediaPropertyView(v any) *MediaView {
	m := obj(v)
	if m == nil {
		return nil
	}
	return &MediaView{TitleYear: text(m["title_year"]), Title: text(m["title"]), MessageImage: text(m["message_image"]), PosterPath: text(m["poster_path"]), Poster: text(m["poster"])}
}
func TestOriginalAllMessageChainEntries(t *testing.T) {
	f, e := os.Open(originalFixture("testdata/source-oracle.jsonl.gz"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	d := json.NewDecoder(z)
	count := 0
	for {
		var c oracleCase
		if e = d.Decode(&c); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		if c.Kind != "module_calls" {
			continue
		}
		count++
		t.Run(str(count), func(t *testing.T) {
			input, e := DecodeSourceObject(c.Input)
			if e != nil {
				t.Fatal(e)
			}
			args := obj(input["args"])
			h := &moduleAlgorithmHost{algorithmHost{calls: []any{}}}
			p := New(PluginHost{API: h})
			if _, e = p.Init(obj(input["config"])); e != nil {
				t.Fatal(e)
			}
			var got any
			var notification Notification
			if n := obj(args["notification"]); n != nil {
				if e = json.Unmarshal(uuidJSON(n), &notification); e != nil {
					t.Fatal(e)
				}
			}
			ctx := context.Background()
			switch input["method"] {
			case "post_message":
				e = p.Post(ctx, notification)
			case "send_direct_message":
				got, e = p.SendDirect(ctx, notification)
			case "post_medias_message":
				medias := []MediaView{}
				for _, v := range arr(args["medias"]) {
					medias = append(medias, *mediaPropertyView(v))
				}
				e = p.PostMedias(ctx, notification, medias)
			case "post_torrents_message":
				torrents := []TorrentView{}
				for _, v := range arr(args["torrents"]) {
					m := obj(v)
					torrents = append(torrents, TorrentView{Title: text(m["title"]), SiteName: text(m["site_name"]), Media: mediaPropertyView(m["media"])})
				}
				e = p.PostTorrents(ctx, notification, torrents)
			case "edit_message":
				got, e = p.Edit(ctx, text(args["channel"]), text(args["source"]), text(args["message_id"]), text(args["chat_id"]), text(args["title"]), text(args["text"]), nil, obj(args["metadata"]))
			case "finalize_message":
				got, e = p.Finalize(ctx, text(args["channel"]), obj(args["metadata"]))
			case "mark_message_processing_started":
				got, e = p.ProcessingStarted(ctx, text(args["channel"]), text(args["source"]), args["userid"], text(args["message_id"]), text(args["chat_id"]), text(args["text"]))
			case "mark_message_processing_finished":
				got, e = p.ProcessingFinished(ctx, text(args["channel"]), text(args["source"]), text(args["message_id"]), obj(args["status"]))
			case "download_feishu_image_to_data_url":
				got, e = p.DownloadImage(ctx, text(args["image_ref"]), text(args["source"]))
			case "download_feishu_file_bytes":
				var b []byte
				b, e = p.DownloadFile(ctx, text(args["file_ref"]), text(args["source"]))
				if b != nil {
					got = Object{"base64": base64.StdEncoding.EncodeToString(b)}
				}
			case "add_feishu_message_reaction":
				got, e = p.AddReaction(ctx, text(args["message_id"]), text(args["emoji_type"]), text(args["source"]))
			case "delete_feishu_message_reaction":
				got, e = p.DeleteReaction(ctx, text(args["message_id"]), text(args["reaction_id"]), text(args["source"]))
			case "message_parser":
				got, e = p.ParseMessageBody(ctx, text(args["source"]), args["body"])
			default:
				t.Fatal(input["method"])
			}
			if c.Error != "" {
				if e == nil {
					t.Fatal("source exception became success", c.Error, got)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			want := asInput[Object](t, c.Output)
			want["calls"] = canonicalSourceCalls(arr(want["calls"]))
			equalJSON(t, uuidJSON(want), Object{"result": got, "calls": canonicalSourceCalls(h.calls)})
		})
	}
	if count != 26 {
		t.Fatal(count)
	}
}
