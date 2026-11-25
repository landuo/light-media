package plugins

import "testing"

func TestModuleScopeExcludesOnlyDedicatedWorkflowAndAIEvents(t *testing.T) {
	base := Manifest{SchemaVersion: 1, PluginID: "OwnedScope", Name: "Owned scope", Version: "1.0", Runtime: "go-executable", Entry: "worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"stop", "event"}}
	for _, kind := range []string{"workflow.execute", "workflow.execution", "agent.tokens.usage", "agent.llm.provider"} {
		for _, eventKind := range []string{BroadcastEventKind, ChainEventKind} {
			if (EventQuery{Kind: eventKind, Type: kind}).Valid() || (EventControl{Action: "add", Kind: eventKind, Type: kind, Method: "event"}).Valid() {
				t.Fatal("excluded event extension point accepted", kind)
			}
		}
		for _, declaration := range []string{"shorthand", "chain", "broadcast"} {
			m := base
			switch declaration {
			case "shorthand":
				m.Events = []string{kind}
			case "chain":
				m.ChainHandlers = []ChainHandler{{Event: kind, Method: "event"}}
			case "broadcast":
				m.BroadcastHandlers = []BroadcastHandler{{Event: kind, Method: "event"}}
			}
			if m.Validate() == nil {
				t.Fatal("excluded manifest extension point accepted", kind, declaration)
			}
		}
	}
	for _, kind := range []string{"plugin.reload", "plugin.data.reset", "command.register", "message.action", "owned.workflow.observation", "owned.agent.status"} {
		if !(EventQuery{Kind: BroadcastEventKind, Type: kind}).Valid() || !(EventQuery{Kind: ChainEventKind, Type: kind}).Valid() {
			t.Fatal("ordinary event removed", kind)
		}
	}
	for _, method := range []string{"get_actions", "get_agent_tools"} {
		m := base
		m.Methods = append(append([]string(nil), base.Methods...), method)
		if m.Validate() == nil {
			t.Fatal("excluded standard getter accepted", method)
		}
	}
}
