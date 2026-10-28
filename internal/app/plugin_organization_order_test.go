package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestOriginalCloneUsesPersistedFolderInsertionOrder(t *testing.T) {
	a, cookie, source := originalBaseSDKFixture(t)
	id := source.installation.InstanceID
	raw := []byte(`{"z-first":{"plugins":["` + id + `"],"showIcon":false},"a-second":["` + id + `"]}`)
	response := organizationCompatRequest(a, cookie, "POST", "/api/v1/plugin/folders", raw, "http://example.test")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	read := organizationCompatRequest(a, cookie, "GET", "/api/v1/plugin/folders", nil, "")
	if keys := originalFolderInputKeys(read.Body.Bytes()); !slices.Equal(keys, []string{"z-first", "a-second"}) {
		t.Fatal("folder GET lost original order", keys)
	}
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/clone", `{"suffix":"folder"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	org, err := a.pluginStore.Organization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := pluginFolderMembers(org.Folders["z-first"])
	second, _ := pluginFolderMembers(org.Folders["a-second"])
	if !slices.Equal(first, []string{id, id + "folder"}) || !slices.Equal(second, []string{id}) {
		t.Fatal("clone joined wrong folder", first, second)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	org, err = reopened.pluginStore.Organization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(originalFolderKeys(org), []string{"z-first", "a-second"}) {
		t.Fatal("cold storage lost insertion order")
	}
	// The UI facade must also preserve raw order on input and output, and
	// deleting/recreating a key appends it exactly as an ordinary Python dict.
	response = callTest(t, reopened.Handler(), "PUT", "/api/plugins/organization", `{"folders":{"z-third":[],"a-fourth":[]}}`, cookie, "")
	if response.Code != 200 || strings.Index(response.Body.String(), "z-third") > strings.Index(response.Body.String(), "a-fourth") {
		t.Fatal(response.Body.String())
	}
	for _, method := range []string{"DELETE", "POST"} {
		response = organizationCompatRequest(reopened, cookie, method, "/api/v1/plugin/folders/z-third", nil, "http://example.test")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	read = organizationCompatRequest(reopened, cookie, "GET", "/api/v1/plugin/folders", nil, "")
	if keys := originalFolderInputKeys(json.RawMessage(read.Body.Bytes())); !slices.Equal(keys, []string{"a-fourth", "z-third"}) {
		t.Fatal(keys)
	}
}

func TestPluginOrganizationOpaqueMembersFirstRemovalAndCloneGuard(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	input := PluginOrganization{Folders: map[string]json.RawMessage{"A": json.RawMessage(`[17,"source",false,"source",{"id":"source"}]`), "ignored": json.RawMessage(`23`)}, Order: json.RawMessage(`[]`)}
	if _, err := a.pluginStore.SaveOrganization(ctx, input); err != nil {
		t.Fatal(err)
	}
	mutate := func(clone bool) {
		t.Helper()
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if clone {
			err = a.pluginStore.cloneOrganizationMemberTx(ctx, tx, "source", "copy")
		} else {
			err = a.pluginStore.removeOrganizationMemberTx(ctx, tx, "source")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	mutate(true)
	mutate(true)
	mutate(false)
	var reference struct {
		Membership []struct{ Folders map[string]json.RawMessage } `json:"membership_cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugins/plugin-organization-api-original-reference.json")
	if err != nil || json.Unmarshal(raw, &reference) != nil || len(reference.Membership) != 3 {
		t.Fatal("fixed source membership replay", err)
	}
	expected := reference.Membership[2].Folders["A"]
	got, err := a.pluginStore.Organization(ctx)
	if err != nil || !originalBaseJSONEqual(got.Folders["A"], expected) {
		t.Fatal("fixed list membership/remove/append behavior", string(got.Folders["A"]), err)
	}
}
