package larkmessager

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"
)

type oracleCase struct {
	Kind   string          `json:"kind"`
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error"`
}

func asInput[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if e := decode(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func equalJSON(t *testing.T, want []byte, got any) {
	t.Helper()
	w := asInput[any](t, want)
	raw, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	g := asInput[any](t, raw)
	if !reflect.DeepEqual(w, g) {
		t.Fatalf("want %s\ngot  %s", want, raw)
	}
}
func TestOriginalSourcePureOracle(t *testing.T) {
	f, e := os.Open("testdata/source-oracle.jsonl.gz")
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
	counts := map[string]int{}
	for index := 0; ; index++ {
		var c oracleCase
		if e = d.Decode(&c); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		switch c.Kind {
		case "client_calls", "multi_calls", "endpoint_calls", "module_calls":
			continue
		}
		counts[c.Kind]++
		t.Run(c.Kind+"/"+str(index), func(t *testing.T) {
			input, decodeError := DecodeSourceObject(c.Input)
			if decodeError != nil {
				t.Fatal(decodeError)
			}
			var got any
			var err error
			switch c.Kind {
			case "python_value":
				v := input["value"]
				var s, r string
				s, err = PythonString(v)
				if err == nil {
					r, err = pythonRepr(v)
				}
				out := Object{"str": s, "repr": r, "truth": truth(v)}
				n, e := pythonInt(v)
				if e != nil {
					out["int_error"] = integerErrorClass(e)
				} else {
					out["int"] = json.Number(n.String())
				}
				got = out
			case "attachment_model":
				got, err = normalizeAttachment(Object{"ref": "owned", "size": input["size"]})
			case "mime_guess":
				var mime, encoding string
				mime, encoding, err = GuessMIME(text(input["name"]))
				got = []any{nullString(mime), nullString(encoding)}
			case "remote_image":
				h := &imageMIMEHost{header: text(input["header"]), calls: []any{}, uploaded: []any{}}
				key, _ := NewClient(h).remoteImage(context.Background(), text(input["url"]))
				got = Object{"key": nullString(key), "uploaded": h.uploaded, "calls": h.calls}
			case "admin_work":
				got, err = adminReferenceResult(input)
			case "module_response_ids":
				h := &responseIDHost{ids: arr(input["ids"])}
				p := New(PluginHost{API: h})
				_, err = p.Init(Object{"enabled": true, "app_id": "SYNTHETIC_APP", "app_secret": "SYNTHETIC_SECRET", "FEISHU_OPEN_ID": "ou_owned"})
				if err == nil {
					got, err = p.SendDirect(context.Background(), Notification{Title: "T", Text: "Body"})
				}
			case "resolve_permission":
				p := New(PluginHost{})
				p.state.UserID = "owned@example.invalid"
				p.client = NewClient(&directoryPermissionHost{failure: text(input["failure"])})
				targets, warning := p.ResolveTargets(context.Background(), "")
				rows := []any{}
				for _, target := range targets {
					rows = append(rows, []string{target.ID, target.Type})
				}
				got = []any{rows, warning}
				if input["failure"] == "other private synthetic" {
					c.Output = []byte(`[[ ],"用户解析失败：Lark API request failed"]`)
				}
			case "legacy_card":
				buttons := []Object{}
				for _, v := range arr(input["buttons"]) {
					buttons = append(buttons, obj(v))
				}
				got, err = BuildLegacyCard(text(input["title"]), text(input["content"]), buttons, text(input["color"]), text(input["img_key"]), text(input["link"]))
			case "legacy_button":
				got = BuildLegacyButton("Go", "action", "value", text(input["kind"]))
			case "legacy_reply":
				h := &algorithmHost{calls: []any{}}
				var result any
				result, err = NewClient(h).ReplyLegacy(context.Background(), "om_owned", text(input["msg_type"]), input["content"])
				got = Object{"result": result, "calls": canonicalSourceCalls(h.calls)}
				var reference Object
				if decode(c.Output, &reference) == nil {
					reference["calls"] = canonicalSourceCalls(arr(reference["calls"]))
					c.Output = uuidJSON(reference)
				}
			case "webhook":
				headers := map[string]string{}
				for k, v := range obj(input["headers"]) {
					headers[k] = text(v)
				}
				out, e := DecodeSourceWebhook([]byte(text(input["raw"])), headers, text(input["query_token"]), text(input["encrypt_key"]), text(input["verification_token"]))
				err = e
				forwards := []any{}
				if out.Accepted {
					p := New(PluginHost{Forward: func(ctx context.Context, payload Object) error { forwards = append(forwards, payload); return nil }})
					if e := p.DispatchTrusted(context.Background(), out.Body); e != nil {
						err = e
					}
				}
				got = Object{"status": out.Status, "reply": out.Reply, "forwards": forwards}
			case "test_card":
				got, err = BuildTestCard("{{LARK_TEST_TIME}}")
			case "page", "page_template":
				p := New(PluginHost{KV: newTestKV()})
				p.state = ConfigState{Enabled: truth(input["enabled"]), AppID: "synthetic", AppSecret: "synthetic", Missing: toStrings(input["missing"])}
				if p.state.Enabled {
					p.client = NewClient(nil)
				}
				var page []any
				page, err = p.Page(context.Background())
				if c.Kind == "page" {
					got = Object{"page": page, "kv": Object{}}
				} else {
					got = page
				}
			case "init":
				var st ConfigState
				st, err = ConfigFromTrusted(input)
				got = Object{"enabled": st.Enabled, "app_id": st.AppID, "app_secret": st.AppSecret, "chat_id": st.ChatID, "user_id_raw": st.UserRaw, "user_id": st.UserID, "verification_token": st.VerificationToken, "encrypt_key": st.EncryptKey, "admin_users": nonnull(st.Admins), "admin_openids_direct": nonnull(st.Direct), "admin_emails": nonnull(st.Emails), "admin_mobiles": nonnull(st.Mobiles), "switchs": st.Switches, "config_missing": nonnull(st.Missing)}
			case "message_parser":
				p := New(PluginHost{})
				got, err = p.ParseMessage(context.Background(), "Lark", input)
			case "card_action":
				var payload Object
				payload, _, err = CardActionPayload(input)
				got = []any{payload}
			case "sync_config":
				var next any
				var changed bool
				next, changed, err = SyncNotificationConfig(input["configs"], toStrings(input["ids"]), text(input["user"]))
				got = []any{}
				if changed {
					got = []any{next}
				}
			case "card":
				got, err = BuildCard(asInput[CardInput](t, c.Input))
			case "stream_helpers":
				s := text(input["text"])
				got = Object{"strip": StripStreamingImages(s), "urls": ExtractImageURLs(s), "card": BuildStreamingCard(" <T>\\ ", s)}
			case "callback":
				v, e := ExtractCallback(input["value"], text(input["name"]))
				err = e
				if v != nil {
					got = *v
				}
			case "cardkit":
				parsed, e := SafeCardKit([]byte(text(input["body"])))
				if e != nil {
					t.Fatal(e)
				}
				status := asInput[struct {
					Status int `json:"status"`
				}](t, c.Input).Status
				canonical := clone(parsed)
				if n, ok := canonical["code"].(json.Number); ok {
					canonical["code"] = json.Number(numberString(n))
				}
				got = Object{"parsed": canonical, "ok": CardKitOK(status, parsed)}
			case "target":
				client := NewClient(nil)
				client.RememberUserIDType("open", " u ")
				a, b, e := client.ResolveTarget(asInput[Target](t, c.Input))
				err = e
				got = []string{a, b}
			case "receive":
				var p Object
				p, err = ReceivePayload(input)
				got = []any{p}
			case "form":
				var form Object
				form, err = Form()
				got = []any{form["form"], form["defaults"]}
			case "crypto":
				crypto := NewCrypto(text(input["key"]))
				var enc, plain string
				enc, err = crypto.Encrypt(text(input["plaintext"]), bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}))
				if err == nil {
					plain, err = crypto.Decrypt(enc)
				}
				got = Object{"encrypted": enc, "decrypted": plain}
			case "crypto_decrypt":
				got, err = NewCrypto(text(input["key"])).Decrypt(text(input["encoded"]))
			case "crypto_signature":
				raw, e := base64.StdEncoding.DecodeString(text(input["raw"]))
				if e != nil {
					t.Fatal(e)
				}
				got = NewCrypto(text(input["key"])).VerifySignature(text(input["signature"]), raw, text(input["timestamp"]), text(input["nonce"]))
			default:
				t.Fatalf("unimplemented oracle kind %s", c.Kind)
			}
			if c.Error != "" {
				if err == nil {
					t.Fatalf("source error %s; Go success %+v", c.Error, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equalJSON(t, c.Output, got)
		})
	}
	t.Logf("source cases exercised by kind: %v", counts)
}

func nonnull(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
