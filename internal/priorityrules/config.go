package priorityrules

import "strings"

// These are the actual CustomRule schema fields. Extended trusted builtin
// definitions are not powers that a public settings payload can introduce.
func ValidateConfiguration(config Configuration) error {
	if len(config.TorrentPriority) > 64 {
		return sourceError("RuleLimit")
	}
	for _, key := range config.TorrentPriority {
		switch key {
		case "torrent", "site", "upload", "seeder":
		default:
			return sourceError("InvalidTorrentPriority")
		}
	}
	if _, err := New(config); err != nil {
		return err
	}
	for _, list := range [][]string{config.SearchGroups, config.SubscribeGroups} {
		if len(list) > 1000 {
			return sourceError("RuleLimit")
		}
		for _, name := range list {
			if len(name) > 512 || strings.ContainsAny(name, "\x00\r\n") {
				return sourceError("InvalidRuleGroup")
			}
		}
	}
	for _, group := range config.Groups {
		if group.Name == "" || len(group.Name) > 512 || strings.ContainsAny(group.Name, "\x00\r\n") {
			return sourceError("InvalidRuleGroup")
		}
	}
	for _, rule := range config.CustomRules {
		for key, value := range rule {
			switch key {
			case "id", "name", "include", "exclude", "size_range", "seeders", "publish_time":
				if value != nil {
					if _, ok := value.(string); !ok {
						return sourceError("InvalidRuleValue")
					}
				}
			default:
				return sourceError("InvalidRuleField")
			}
		}
	}
	return nil
}
