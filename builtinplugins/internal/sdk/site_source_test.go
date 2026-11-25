package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSourceSitesClientMandatoryCurrentDTOAndIdentity(t *testing.T) {
	for _, wire := range []string{`{"schema_version":1,"data":null}`, `{"schema_version":1,"data":{"userid":90071992547409931234567890,"seeding_info":[null,{}],"err_msg":null}}`} {
		host := func(_ context.Context, m string, r json.RawMessage) (json.RawMessage, error) {
			if m != SitesSourceRefreshCapability {
				t.Fatal(m)
			}
			return json.RawMessage(wire), nil
		}
		out, err := (Sites{Host: host}).SourceRefresh(context.Background(), "owned")
		if err != nil || !strings.Contains(wire, string(out.Data)) {
			t.Fatal("current source DTO lost", out, err)
		}
	}
	for _, wire := range []string{`{"schema_version":1}`, `{"schema_version":1,"data":[]}`, `{"schema_version":2,"data":null}`} {
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(wire), nil
		}
		if _, err := (Sites{Host: host}).SourceRefreshAll(context.Background()); err == nil {
			t.Fatal("accepted invalid current source data", wire)
		}
	}
	denied := errors.New("owned scope denied")
	if _, err := (Sites{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, denied }}).SourceRefreshAll(context.Background()); !errors.Is(err, denied) {
		t.Fatal("guard failure swallowed", err)
	}
	for _, wire := range []string{`{"schema_version":1,"indexer":null}`, `{"schema_version":1,"indexer":{"site_id":null,"fields":{"domain":"https://owned.invalid/"}}}`, `{"schema_version":1,"indexer":{"site_id":"owned","fields":{"id":7}}}`} {
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(wire), nil
		}
		if _, err := (Sites{Host: host}).SourceIndexer(context.Background(), "raw.key"); err != nil {
			t.Fatal(wire, err)
		}
	}
}

func TestSourceSitesRejectMissingIndexerAndKeepSourceAccounts(t *testing.T) {
	for _, wire := range []string{`{"schema_version":1}`, `{"schema_version":1,"indexer":[]}`, `{"schema_version":1,"indexer":{"site_id":"","fields":{}}}`} {
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(wire), nil
		}
		if _, err := (Sites{Host: host}).SourceIndexer(context.Background(), "owned"); err == nil {
			t.Fatal("accepted missing/invalid indexer", wire)
		}
	}
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != SitesSourceAccountsCapability || !strings.Contains(string(raw), `"latest":true`) {
			t.Fatal("source account request changed", method, string(raw))
		}
		return json.RawMessage(`{"schema_version":1,"items":[{"site_id":"owned","fields":{"name":"StoredName","userid":90071992547409931234567890,"bonus":null}}]}`), nil
	}
	out, err := (Sites{Host: host}).SourceAccounts(context.Background(), SiteAccountHostRequestV1{ID: "owned", Latest: true})
	if err != nil || len(out.Items) != 1 || !strings.Contains(string(out.Items[0].Fields), "90071992547409931234567890") {
		t.Fatal(out, err)
	}
}
