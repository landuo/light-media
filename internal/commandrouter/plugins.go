package commandrouter

import "strings"

// PluginCommand is one installed plugin's original get_command declaration.
// The source dispatches it as EventType.PluginAction with data.action; the host
// still needs its own authenticated ingress before executing anything.
type PluginCommand struct {
	Name        string
	PluginID    string
	Action      string
	Description string
}

// pluginCommands is frozen from the installed original sources:
//
//	AutoSignIn/__init__.py          04044049166caa06659249b167539f96f1b3505d57da3fbf379ba4472a84008d
//	CleanInvalidSeed/__init__.py    fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda
//	CloudflareSpeedTest/__init__.py 2e4549b9181e8714c401f18f1b082f539d1d8c20fa32c9675b994b6277b106f1
//
// The remaining installed plugins either return None or only contain the
// documented "/xx" template. An empty lookup is therefore not proof that no
// plugin command exists in a newer installation.
func pluginCommands() []PluginCommand {
	return []PluginCommand{
		{Name: "/site_signin", PluginID: "AutoSignIn", Action: "site_signin", Description: "站点自动签到"},
		{Name: "/detect_invalid_torrents", PluginID: "CleanInvalidSeed", Action: "detect_invalid_torrents", Description: "检测无效种子"},
		{Name: "/cloudflare_speedtest", PluginID: "CloudflareSpeedTest", Action: "cloudflare_speedtest", Description: "Cloudflare IP优选"},
	}
}

// LookupPluginCommand resolves a plugin command. Names stay case-sensitive, the
// same as the source command table; arguments are not part of the lookup.
func LookupPluginCommand(name string) (PluginCommand, bool) {
	for _, command := range pluginCommands() {
		if command.Name == name {
			return command, true
		}
	}
	return PluginCommand{}, false
}

// PluginCommands returns a copy so callers cannot mutate the registry.
func PluginCommands() []PluginCommand {
	return append([]PluginCommand{}, pluginCommands()...)
}

// CommandTarget keeps a command bound to its declaring plugin even when the
// plugin publishes several actions.
func (c PluginCommand) Target(action string) bool {
	return c.Action == strings.TrimSpace(action)
}
