package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOriginalTransferInterceptFixedCallerActualSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/transfer-intercept-oracle.json")
	var oracle struct {
		Cases []struct {
			Name, Reason          string
			Patch                 map[string]any
			Fail, Over, Cancelled bool
			Events                []struct {
				Type  string
				Input json.RawMessage
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed actual interception oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			id := installOwnedTransferObserver(t, a, map[string]map[string]any{"transfer.intercept": sample.Patch}, map[string]bool{"transfer.intercept": sample.Fail})
			path := filepath.Join(source, "Old.mkv")
			importWrite(t, path, []byte("0123456789"))
			stamp := time.Unix(1712345678, 123456789)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			file := PlanFile{Source: path, Target: filepath.Join(target, "New.mkv"), Method: "hardlink"}
			projection := originalTransferFileContext{Meta: json.RawMessage(`{"title":"meta"}`), Media: json.RawMessage(`{"title":"media"}`)}
			err := a.originalTransferIntercept(context.Background(), file, projection, sample.Over)
			if (err != nil) != sample.Cancelled || err != nil && err.Error() != sample.Reason {
				t.Fatal("fixed source cancellation", err, sample.Reason)
			}
			a.wg.Wait()
			observed := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var calls []struct {
				Type string
				Data map[string]json.RawMessage
			}
			if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &calls) != nil || len(calls) != 1 {
				t.Fatal("real SDK callback", observed.Body.String())
			}
			var item map[string]json.RawMessage
			if json.Unmarshal(calls[0].Data["fileitem"], &item) != nil {
				t.Fatal("actual local DTO")
			}
			item["path"] = json.RawMessage(`"/owned-source/Old.mkv"`)
			calls[0].Data["fileitem"], _ = json.Marshal(item)
			calls[0].Data["target_path"] = json.RawMessage(`"/owned-target/New.mkv"`)
			got, _ := json.Marshal(calls[0].Data)
			if calls[0].Type != sample.Events[0].Type || !originalBaseJSONEqual(got, sample.Events[0].Input) {
				t.Fatal("fixed complete SDK input", string(got), string(sample.Events[0].Input))
			}
		})
	}
}
