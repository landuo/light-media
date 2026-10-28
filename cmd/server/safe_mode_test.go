package main

import "testing"

func TestPluginMarketEnvironmentPresence(t *testing.T) {
	if pluginMarketEnvironment(func(string) (string, bool) { return "", false }) != nil {
		t.Fatal("missing env became override")
	}
	for _, raw := range []string{"", "https://one.invalid/index, https://two.invalid/index,"} {
		got := pluginMarketEnvironment(func(key string) (string, bool) {
			if key != "PLUGIN_MARKET" {
				t.Fatal(key)
			}
			return raw, true
		})
		if got == nil {
			t.Fatal("present env ignored")
		}
		if raw == "" && len(*got) != 0 {
			t.Fatal(*got)
		}
		if raw != "" && (len(*got) != 2 || (*got)[1] != "https://two.invalid/index") {
			t.Fatal(*got)
		}
	}
}

func TestSafeModeStartupConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		want    bool
		invalid bool
	}{
		{"default", nil, false, false},
		{"native", map[string]string{"LIGHT_MEDIA_SAFE_MODE": "true"}, true, false},
		{"original", map[string]string{"MOVIEPILOT_SAFE_MODE": "YES"}, true, false},
		{"native overrides original", map[string]string{"LIGHT_MEDIA_SAFE_MODE": "off", "MOVIEPILOT_SAFE_MODE": "true"}, false, false},
		{"invalid native does not fall back", map[string]string{"LIGHT_MEDIA_SAFE_MODE": "", "MOVIEPILOT_SAFE_MODE": "true"}, false, true},
		{"invalid original", map[string]string{"MOVIEPILOT_SAFE_MODE": "maybe"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := safeMode(func(key string) (string, bool) { value, exists := tc.env[key]; return value, exists })
			if got != tc.want || (err != nil) != tc.invalid {
				t.Fatalf("got %v, %v; want %v, invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
