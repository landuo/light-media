package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestCustomHostsInstalledSourceOracle(t *testing.T) {
	var oracle struct {
		SourceSHA string            `json:"source_sha256"`
		Form      []json.RawMessage `json:"form"`
		Cases     []struct {
			Name       string          `json:"name"`
			Config     json.RawMessage `json:"config"`
			Original   string          `json:"original"`
			WriteError bool            `json:"write_error"`
			Result     struct {
				Content  string            `json:"content"`
				Reads    int               `json:"reads"`
				Writes   int               `json:"writes"`
				Saved    []json.RawMessage `json:"saved"`
				Messages []map[string]any  `json:"messages"`
				State    any               `json:"state"`
				Error    any               `json:"error"`
			} `json:"result"`
		} `json:"cases"`
	}
	b, e := os.ReadFile("testdata/custom-hosts-original.json")
	if e != nil || json.Unmarshal(b, &oracle) != nil || oracle.SourceSHA != "0d6561baa5fdeddac87039718e10648c6df2deed52bbe6397f807cf3862560ca" {
		t.Fatal("invalid installed oracle", e)
	}
	form, e := HandleCustomHosts(context.Background(), "config_form", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	var actualForm struct {
		Conf  any `json:"conf"`
		Model any `json:"model"`
	}
	_ = json.Unmarshal(form, &actualForm)
	var wantConf, wantModel any
	_ = json.Unmarshal(oracle.Form[0], &wantConf)
	_ = json.Unmarshal(oracle.Form[1], &wantModel)
	if !reflect.DeepEqual(actualForm.Conf, wantConf) || !reflect.DeepEqual(actualForm.Model, wantModel) {
		t.Fatal("original form changed")
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			reads, writes := 0, 0
			content := c.Original
			saved := []json.RawMessage{}
			messages := []map[string]any{}
			host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case "hosts.read":
					reads++
					b, _ := json.Marshal(map[string]string{"content": content, "digest": "owned"})
					return b, nil
				case "hosts.replace":
					writes++
					if c.WriteError {
						return nil, errors.New("owned denied")
					}
					var x struct {
						Content string `json:"content"`
					}
					if json.Unmarshal(raw, &x) != nil {
						t.Fatal("invalid request")
					}
					content = x.Content
					return json.RawMessage(`{"applied":true}`), nil
				case "config.patch":
					saved = append(saved, append(json.RawMessage{}, raw...))
					return json.RawMessage(`{"saved":true}`), nil
				case "notification.post":
					var x struct {
						Title    string   `json:"title"`
						Message  string   `json:"message"`
						Messages []string `json:"messages"`
					}
					_ = json.Unmarshal(raw, &x)
					if x.Message != "" {
						messages = append(messages, map[string]any{"title": x.Title, "message": x.Message})
					}
					for _, m := range x.Messages {
						messages = append(messages, map[string]any{"title": x.Title, "message": m})
					}
					return json.RawMessage(`{"posted":true}`), nil
				}
				return nil, errors.New("unexpected host method")
			}
			request, _ := json.Marshal(map[string]any{"action": "init", "config": c.Config})
			_, e := HandleCustomHosts(context.Background(), "invoke", request, host)
			// Original catches filesystem write errors and reports them through system
			// messages. The native run also surfaces the failed side effect as an error.
			if c.Result.Error != nil {
				if e == nil {
					t.Fatal("source exception became success")
				}
				return
			}
			if e != nil && !c.WriteError {
				t.Fatal("source returned normally", e)
			}
			if reads != c.Result.Reads || writes != c.Result.Writes {
				t.Errorf("IO attempts got read=%d write=%d want read=%d write=%d", reads, writes, c.Result.Reads, c.Result.Writes)
			}
			if content != c.Result.Content {
				t.Errorf("file differs\ngot %q\nwant%q", content, c.Result.Content)
			}
			var gotSaved, wantSaved any
			gb, _ := json.Marshal(saved)
			wb, _ := json.Marshal(c.Result.Saved)
			_ = json.Unmarshal(gb, &gotSaved)
			_ = json.Unmarshal(wb, &wantSaved)
			if !reflect.DeepEqual(gotSaved, wantSaved) {
				t.Errorf("source config update differs got %s want %s", gb, wb)
			}
			if !reflect.DeepEqual(messages, c.Result.Messages) {
				t.Errorf("source system messages differ got %#v want %#v", messages, c.Result.Messages)
			}
		})
	}
}
