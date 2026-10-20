package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/releasemeta"
)

func TestOriginalLarkMediaMessageProtocolAST(t *testing.T) {
	root := "../../testdata/plugins/lark-message-chain-reference"
	raw, e := os.ReadFile(filepath.Join(root, "message-protocol-oracle.json"))
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Python, Unicode, Cn2an string
		SourceSHA              map[string]string `json:"source_sha256"`
		Cases                  []struct {
			Kind   string
			Input  map[string]json.RawMessage
			Output json.RawMessage
			Error  string
		}
	}
	if json.Unmarshal(raw, &corpus) != nil || corpus.Unicode != "15.0.0" || len(corpus.Cases) != 132 {
		t.Fatal("incomplete actual source corpus", len(corpus.Cases))
	}
	for path, want := range corpus.SourceSHA {
		b, e := os.ReadFile(filepath.Join(root, "source", path))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("source changed", path)
		}
	}
	for i, c := range corpus.Cases {
		t.Run(c.Kind+"/"+notificationChannelRevisionKey(int64(i)), func(t *testing.T) {
			text := ""
			_ = json.Unmarshal(c.Input["text"], &text)
			var value any
			var err error
			switch c.Kind {
			case "action":
				action, content := releasemeta.ResolveMediaAction(text)
				var a any
				if action != "" {
					a = action
				}
				value = map[string]any{"action": a, "content": content}
			case "keyword":
				out, e := releasemeta.SearchInputKeyword(text)
				err = e
				str := func(s string) any {
					if s == "" {
						return nil
					}
					return s
				}
				var season, episode any
				if out.Season != nil {
					season = out.Season.String()
				}
				if out.Episode != nil {
					episode = out.Episode.String()
				}
				var content any
				if out.Present {
					content = out.Content
				}
				value = map[string]any{"type": str(out.Type), "keyword": str(out.Keyword), "season": season, "episode": episode, "year": str(out.Year), "content": content}
			case "callback":
				out, e := larkParseMediaCallback(text)
				err = e
				if out != nil {
					var request, index any
					if out.Request != nil {
						request = *out.Request
					}
					if out.Index != nil {
						index = out.Index.String()
					}
					value = map[string]any{"request": request, "action": out.Action, "index": index}
				}
			case "page":
				var total, page int
				_ = json.Unmarshal(c.Input["total"], &total)
				_ = json.Unmarshal(c.Input["page"], &page)
				start, end, current, pages := larkMediaPage(total, page)
				items := []int{}
				for n := start; n < end; n++ {
					items = append(items, n)
				}
				value = map[string]any{"items": items, "page": current, "pages": pages}
			case "buttons":
				var phase string
				var count, total, page, pages int
				_ = json.Unmarshal(c.Input["phase"], &phase)
				_ = json.Unmarshal(c.Input["count"], &count)
				_ = json.Unmarshal(c.Input["total"], &total)
				_ = json.Unmarshal(c.Input["page"], &page)
				_ = json.Unmarshal(c.Input["pages"], &pages)
				value = larkMediaButtons("ownedrequest", phase, count, total, page, pages)
			default:
				t.Fatal("unconsumed source case", c.Kind)
			}
			if c.Error != "" {
				if err == nil {
					t.Fatal("source exception became success", c.Error, value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(value)
			var actual, expected any
			if json.Unmarshal(got, &actual) != nil || json.Unmarshal(c.Output, &expected) != nil {
				t.Fatal("invalid comparison")
			}
			got, _ = json.Marshal(actual)
			want, _ := json.Marshal(expected)
			if !bytes.Equal(got, want) {
				t.Fatalf("source differs input=%s\ngot=%s\nwant=%s", mustCleanSeedJSON(c.Input), got, want)
			}
		})
	}
}
