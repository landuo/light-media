package larkmessager

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

type algorithmHost struct {
	calls []any
	fault int
	token bool
}

func (h *algorithmHost) record(v Object) error {
	h.calls = append(h.calls, v)
	if len(h.calls) == h.fault {
		return errors.New("synthetic transport fault")
	}
	return nil
}
func (h *algorithmHost) API(ctx context.Context, r Request) (Response, error) {
	if !h.token {
		e := h.record(Object{"method": "POST", "url": APIBase + "/auth/v3/tenant_access_token/internal", "json": Object{"app_id": "SYNTHETIC_APP", "app_secret": "SYNTHETIC_SECRET"}, "timeout": 10})
		if e != nil {
			return Response{}, e
		}
		h.token = true
	}
	method, path, timeout, _, e := route(r)
	if e != nil {
		return Response{}, e
	}
	payload := clone(r.Payload)
	query := clone(r.Query)
	if query == nil {
		query = Object{}
	}
	switch r.Op {
	case "send", "reply":
		payload["content"] = string(uuidJSON(payload["content"]))
		payload["uuid"] = "11111111-2222-4333-8444-555555555555"
		if r.Op == "send" {
			query["receive_id_type"] = r.IDs[1]
		}
	case "edit":
		payload["content"] = string(uuidJSON(payload["content"]))
	case "card_create":
		payload = Object{"type": "card_json", "data": string(uuidJSON(payload["card"]))}
	case "card_content":
		payload["uuid"] = "11111111-2222-4333-8444-555555555555"
	case "card_close":
		payload["uuid"] = "11111111-2222-4333-8444-555555555555"
		payload["settings"] = string(uuidJSON(Object{"config": Object{"streaming_mode": false}}))
	}
	headers := Object{"Authorization": "Bearer SYNTHETIC_TOKEN"}
	call := Object{"method": method, "url": APIBase + path, "headers": headers, "timeout": int(timeout.Seconds())}
	if len(query) > 0 {
		call["params"] = query
	}
	if r.Blob != nil {
		key := "file"
		if r.Op == "upload_image" {
			key = "image"
			call["data"] = payload
		}
		call["files"] = Object{key: Object{"name": r.Blob.Name, "base64": base64.StdEncoding.EncodeToString(r.Blob.Data), "mime": r.Blob.MIME}}
	} else {
		headers["Content-Type"] = "application/json"
		if payload != nil {
			call["json"] = payload
		}
	}
	if e = h.record(call); e != nil {
		return Response{}, e
	}
	mtype := text(payload["msg_type"])
	if mtype == "" {
		mtype = "text"
	}
	body := Object{"code": 0, "data": Object{"message_id": "om_reply", "chat_id": "oc_reply", "msg_type": mtype, "root_id": "om_root", "parent_id": "om_parent", "thread_id": "omt_thread", "image_key": "img_owned", "file_key": "file_owned", "card_id": "card_owned", "reaction_id": "r_owned"}}
	return Response{Status: 200, Body: uuidJSON(body)}, nil
}
func (h *algorithmHost) ReadFile(ctx context.Context, ref string) (Blob, error) {
	if !strings.HasPrefix(ref, "owned:") {
		return Blob{}, ErrDenied
	}
	name := strings.TrimPrefix(ref, "owned:")
	mime := "application/octet-stream"
	if strings.HasSuffix(name, ".png") {
		mime = "image/png"
	}
	return Blob{Name: name, MIME: mime, Data: []byte("owned bytes")}, nil
}
func (h *algorithmHost) FetchImage(context.Context, string) (Blob, error) {
	return Blob{}, ErrDependency
}
func canonicalSourceCalls(calls []any) any {
	raw := uuidJSON(calls)
	var v any
	_ = decode(raw, &v)
	var visit func(any) any
	visit = func(v any) any {
		switch v := v.(type) {
		case []any:
			for i, x := range v {
				v[i] = visit(x)
			}
		case Object:
			for k, x := range v {
				if k == "content" || k == "settings" || k == "data" {
					if s, ok := x.(string); ok {
						var parsed any
						if decode([]byte(s), &parsed) == nil {
							x = parsed
						}
					}
				}
				v[k] = visit(x)
			}
		}
		return v
	}
	return visit(v)
}
func resultObject(r *Result) any {
	if r == nil || !r.Success {
		return Object{"success": false}
	}
	d := Object{"success": true, "message_id": r.MessageID, "chat_id": r.ChatID, "msg_type": r.MsgType}
	if r.Metadata != nil {
		d["metadata"] = r.Metadata
	}
	if r.RootID != nil {
		d["root_id"] = r.RootID
		d["parent_id"] = r.ParentID
		d["thread_id"] = r.ThreadID
	}
	return d
}
func TestOriginalSourceMultiStepAlgorithms(t *testing.T) {
	f, e := os.Open("testdata/source-oracle.jsonl.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, _ := gzip.NewReader(f)
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
		if c.Kind != "multi_calls" {
			continue
		}
		count++
		input := asInput[Object](t, c.Input)
		t.Run(text(input["method"])+"/"+str(count), func(t *testing.T) {
			fault := asInput[struct {
				Fault int `json:"fault"`
			}](t, c.Input).Fault
			host := &algorithmHost{fault: fault, calls: []any{}}
			client := NewClient(host)
			args := obj(input["args"])
			var result any
			var err error
			target := Target{UserID: text(args["userid"])}
			switch input["method"] {
			case "send_file":
				var r *Result
				r, err = client.SendFile(context.Background(), text(args["file_path"]), text(args["title"]), text(args["text"]), text(args["file_name"]), target)
				result = resultObject(r)
			case "send_voice":
				var r *Result
				r, err = client.SendVoice(context.Background(), text(args["voice_path"]), text(args["caption"]), target)
				result = resultObject(r)
			case "send_notification":
				var n Notification
				if e := decode(uuidJSON(args["message"]), &n); e != nil {
					t.Fatal(e)
				}
				var r *Result
				r, err = client.SendNotification(context.Background(), n, target, nil)
				result = resultObject(r)
			case "edit_message":
				result, err = client.EditMessage(context.Background(), text(args["message_id"]), text(args["title"]), text(args["text"]), nil, obj(args["metadata"]), "")
			case "close_streaming_card":
				n, _ := integer(args["sequence"])
				v, _ := n.Int64()
				result, err = client.CloseStreaming(context.Background(), text(args["card_id"]), int(v))
			default:
				t.Fatal(input)
			}
			expected := asInput[Object](t, c.Output)
			if _, has := expected["error"]; has {
				if err == nil {
					t.Fatal("source raised but Go succeeded")
				}
			} else {
				equalJSON(t, uuidJSON(expected["result"]), result)
				equalJSON(t, uuidJSON(obj(expected["args_after"])["metadata"]), args["metadata"])
			}
			want, got := canonicalSourceCalls(arr(expected["calls"])), canonicalSourceCalls(host.calls)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("source stage trace differs\nwant %s\ngot %s", uuidJSON(want), uuidJSON(got))
			}
		})
	}
	if count != 40 {
		t.Fatal(count)
	}
}
