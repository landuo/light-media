package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/remoteidentifiers"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRemoteIdentifiersInstalledASTOracleStateAndForm(t *testing.T) {
	raw, err := os.ReadFile("testdata/remote-identifiers-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceSHA map[string]string `json:"source_sha256"`
		State     []struct {
			Name   string
			Config json.RawMessage
			State  json.RawMessage
			Saved  json.RawMessage
		}
		Form   json.RawMessage
		Denied int `json:"denied_network_calls"`
		Writes int `json:"denied_writes"`
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.SourceSHA["remote"] != "14ec7f5f2964ebd1674305d5294ffa56fb61e2cd1021ea31e50d9675813891a4" || oracle.Denied != 0 || oracle.Writes != 0 {
		t.Fatal("untrusted oracle")
	}
	warm := remoteidentifiers.Cold()
	for _, tc := range oracle.State {
		t.Run(tc.Name, func(t *testing.T) {
			state := remoteidentifiers.Cold()
			if strings.HasPrefix(tc.Name, "warm_") {
				state = warm
				tc.Config = json.RawMessage(`{}`)
				if tc.Name == "warm_none" {
					tc.Config = json.RawMessage(`null`)
				}
			}
			if err := state.Init(tc.Config); err != nil {
				t.Fatal(err)
			}
			if remoteidentifiers.Truth(state.OnlyOnce) {
				state.OnlyOnce = false
			}
			got, _ := json.Marshal(state)
			var a, b any
			json.Unmarshal(got, &a)
			json.Unmarshal(tc.State, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("state differs: %s", tc.Name)
			}
			if tc.Name == "all" {
				warm = state
			}
		})
	}
	form, err := builtin.HandleRemoteIdentifiers(context.Background(), "config_form", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	json.Unmarshal(form, &a)
	json.Unmarshal(oracle.Form, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("source form differs")
	}
}
