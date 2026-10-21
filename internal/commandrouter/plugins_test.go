package commandrouter

import "testing"

func TestPluginCommandsMatchInstalledDeclarations(t *testing.T) {
	commands := PluginCommands()
	if len(commands) != 3 {
		t.Fatalf("plugin command count %d", len(commands))
	}
	want := map[string]PluginCommand{
		"/site_signin":             {Name: "/site_signin", PluginID: "AutoSignIn", Action: "site_signin", Description: "站点自动签到"},
		"/detect_invalid_torrents": {Name: "/detect_invalid_torrents", PluginID: "CleanInvalidSeed", Action: "detect_invalid_torrents", Description: "检测无效种子"},
		"/cloudflare_speedtest":    {Name: "/cloudflare_speedtest", PluginID: "CloudflareSpeedTest", Action: "cloudflare_speedtest", Description: "Cloudflare IP优选"},
	}
	for _, command := range commands {
		expected, ok := want[command.Name]
		if !ok || expected != command {
			t.Errorf("unexpected plugin command %#v", command)
		}
	}
	for name, expected := range want {
		got, ok := LookupPluginCommand(name)
		if !ok || got != expected {
			t.Errorf("%s resolved to %#v ok=%v", name, got, ok)
		}
	}
	if _, ok := LookupPluginCommand("/Site_Signin"); ok {
		t.Fatal("plugin command lookup became case-insensitive")
	}
	if _, ok := LookupPluginCommand("/xx"); ok {
		t.Fatal("template command registered")
	}
	if _, ok := LookupPluginCommand("/cloudflare_speedtest extra"); ok {
		t.Fatal("arguments became part of the command name")
	}
}

func TestPluginCommandsAreNotPresets(t *testing.T) {
	for _, command := range PluginCommands() {
		if _, ok := Lookup(command.Name); ok {
			t.Errorf("plugin command %s collides with a builtin preset", command.Name)
		}
	}
}

func TestPluginCommandCopiesAreIndependent(t *testing.T) {
	commands := PluginCommands()
	commands[0].PluginID = "mutated"
	if again := PluginCommands(); again[0].PluginID == "mutated" {
		t.Fatal("plugin registry leaked a caller mutation")
	}
}

func TestPluginCommandTargetBinding(t *testing.T) {
	command, _ := LookupPluginCommand("/cloudflare_speedtest")
	if !command.Target("cloudflare_speedtest") || command.Target("site_signin") || command.Target("") {
		t.Fatalf("target binding %#v", command)
	}
}
