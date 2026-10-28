package plugins

import "testing"

func TestFrontendContractIsOptionalAndBounded(t *testing.T) {
	if (*Frontend)(nil).Validate() != nil {
		t.Fatal("legacy manifest without frontend rejected")
	}
	valid := func() Frontend {
		return Frontend{Contract: FrontendFederationContract, Root: "frontend/assets", Entry: "remoteEntry.js", Exposes: []string{"Config", "AppPage"}, Shared: []string{"vue", "vuetify"}, APIContract: FrontendAPIContract}
	}
	for _, change := range []string{"none", "auth", "custom_auth", "contract", "root", "entry", "unsafe_expose", "duplicate", "shared", "missing_vue", "api"} {
		t.Run(change, func(t *testing.T) {
			f := valid()
			switch change {
			case "contract":
				f.Contract = "execute-anything/v1"
			case "root":
				f.Root = "https://example.invalid/assets"
			case "entry":
				f.Entry = "../remoteEntry.js"
			case "auth":
				f.Exposes = []string{"AuthPage"}
			case "custom_auth":
				f.Exposes = []string{"AccountLogin"}
			case "unsafe_expose":
				f.Exposes = []string{"../AuthPage"}
			case "duplicate":
				f.Exposes = []string{"Config", "Config"}
			case "shared":
				f.Shared = []string{"vue", "https://example.invalid/vue.js"}
			case "missing_vue":
				f.Shared = []string{"vuetify"}
			case "api":
				f.APIContract = "host-unrestricted/v1"
			}
			if err := f.Validate(); (err == nil) != (change == "none" || change == "auth" || change == "custom_auth") {
				t.Fatalf("%s: %v", change, err)
			}
		})
	}
	for _, path := range []string{"", ".", "..", "/assets/a.js", "assets/../a.js", "assets//a.js", "assets/a\\b.js", "assets/.hidden", "assets/a\x00.js", "CON.js", "assets/x."} {
		if ValidFrontendPath(path) {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
	for _, path := range []string{"frontend/assets", "remoteEntry.js", "__federation_expose_Config-123.js", "fonts/inter-v1.woff2", "assets/a%2f.js", "assets/a?b.js", "assets/a#b.js", "assets/a:b.css", "assets/a💫.css"} {
		if !ValidFrontendPath(path) {
			t.Errorf("safe build path rejected: %q", path)
		}
	}
}
