package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"light-media/internal/plugins"
)

// Expected outcomes come from oracle-followup/oracle.py executing the actual
// pinned Command methods and DictUtils. A truthy list/string reaches DictUtils
// and becomes {}; a truthy scalar fails at the original len() before filtering.
// This is an in-process framework regression, not another SDK IPC/provider test.
func TestOriginalCommandRegisterSourceCommandShapes(t *testing.T) {
	cases := []struct {
		name        string
		raw         json.RawMessage
		scalarError bool
	}{
		{"nonempty_list", json.RawMessage(`["not","a","dict"]`), false},
		{"nonempty_string", json.RawMessage(`"not-a-dict"`), false},
		{"empty_list", json.RawMessage(`[]`), false},
		{"empty_string", json.RawMessage(`""`), false},
		{"zero", json.RawMessage(`0`), false},
		{"false", json.RawMessage(`false`), false},
		{"null", json.RawMessage(`null`), false},
		{"nonzero_number", json.RawMessage(`7`), true},
		{"true", json.RawMessage(`true`), true},
		{"unknown_subset", json.RawMessage(`{"/unknown":{"description":"discard"}}`), false},
	}
	for _, test := range cases {
		for _, forced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%t", test.name, forced), func(t *testing.T) {
				a := newTestApp(t)
				previous := map[string]json.RawMessage{"/previous": json.RawMessage(`{"description":"retained"}`)}
				a.pluginOriginalCommands = map[string]map[string]any{"/owned": {"desc": "execution remains", "pid": "Owned"}}
				a.pluginOriginalRegisteredCommands = previous
				a.registerOriginalChainHandler("command.register", originalMutableEventHandler{
					ID: "owned-shape", Instance: "Owned", Priority: 10,
					Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
						source := "Other"
						if forced {
							source = "Owned"
						}
						raw, err := json.Marshal(map[string]any{"commands": test.raw, "cancel": false, "source": source})
						event.Data = raw
						return err
					},
				})
				a.pluginOriginalCommandsMu.Lock()
				decision, err := a.prepareOriginalCommandRegistration(context.Background(), "Owned")
				after, _ := json.Marshal(a.pluginOriginalRegisteredCommands)
				a.pluginOriginalCommandsMu.Unlock()
				old, _ := json.Marshal(previous)
				if test.scalarError {
					if err == nil || errors.Is(err, errOriginalCommandRegisterProviderUnavailable) || decision.ShouldRegister || !originalBaseJSONEqual(after, old) {
						t.Fatalf("truthy scalar must fail before selection: %+v err=%v snapshot=%s", decision, err, after)
					}
				} else if forced {
					if !errors.Is(err, errOriginalCommandRegisterProviderUnavailable) || !decision.ShouldRegister || !decision.Forced || len(decision.Commands) != 0 || !originalBaseJSONEqual(after, json.RawMessage(`{}`)) {
						t.Fatalf("source force-empty decision: %+v err=%v snapshot=%s", decision, err, after)
					}
				} else {
					if err != nil || decision.ShouldRegister || !originalBaseJSONEqual(after, old) {
						t.Fatalf("source skip-empty decision: %+v err=%v snapshot=%s", decision, err, after)
					}
				}
				if a.pluginOriginalCommands["/owned"]["desc"] != "execution remains" {
					t.Fatal("menu shape changed execution metadata")
				}
			})
		}
	}
}

func TestOriginalCommandRegisterShapeFixPreservesSubsetAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		for _, forced := range []bool{false, true} {
			t.Run(fmt.Sprintf("cancel=%t/force=%t", cancelled, forced), func(t *testing.T) {
				a := newTestApp(t)
				previous := map[string]json.RawMessage{"/previous": json.RawMessage(`{"description":"retained"}`)}
				a.pluginOriginalCommands = map[string]map[string]any{"/owned": {"desc": "execution remains", "pid": "Owned"}}
				a.pluginOriginalRegisteredCommands = previous
				a.registerOriginalChainHandler("command.register", originalMutableEventHandler{
					ID: "owned-subset", Instance: "Owned", Priority: 10,
					Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
						source := "Other"
						if forced {
							source = "Owned"
						}
						raw, err := json.Marshal(map[string]any{"commands": json.RawMessage(`{"/owned":{"description":"menu only"},"/unknown":{"description":"discard"}}`), "cancel": cancelled, "source": source})
						event.Data = raw
						return err
					},
				})
				a.pluginOriginalCommandsMu.Lock()
				decision, err := a.prepareOriginalCommandRegistration(context.Background(), "Owned")
				after, _ := json.Marshal(a.pluginOriginalRegisteredCommands)
				a.pluginOriginalCommandsMu.Unlock()
				if cancelled {
					old, _ := json.Marshal(previous)
					if err != nil || !decision.Cancelled || decision.ShouldRegister || decision.Forced || !originalBaseJSONEqual(after, old) {
						t.Fatalf("cancel must win before force/filter: %+v err=%v snapshot=%s", decision, err, after)
					}
				} else {
					if !errors.Is(err, errOriginalCommandRegisterProviderUnavailable) || !decision.ShouldRegister || !originalBaseJSONEqual(after, json.RawMessage(`{"/owned":{"description":"menu only"}}`)) {
						t.Fatalf("known subset must survive: %+v err=%v snapshot=%s", decision, err, after)
					}
				}
				if a.pluginOriginalCommands["/owned"]["desc"] != "execution remains" {
					t.Fatal("registration changed execution metadata")
				}
			})
		}
	}
}
