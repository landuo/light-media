package plugins

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

const FrontendFederationContract = "light-media.vue-federation/v1"
const FrontendAPIContract = "light-media.plugin-api/v1"

// Frontend declares a package ABI, not a URL, an installation, or host authority.
// Files are supplied by the same verified artifact as the worker. The host
// constructs an instance-specific URL only after checking the installed bytes.
type Frontend struct {
	Contract    string   `json:"contract"`
	Root        string   `json:"root"`
	Entry       string   `json:"entry"`
	Exposes     []string `json:"exposes"`
	Shared      []string `json:"shared"`
	APIContract string   `json:"api_contract"`
}

func validFrontendSegment(value string) bool { return validPluginPhysicalComponent(value, true) }

var frontendComponent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// ValidFrontendPath accepts only canonical, bounded package-relative paths.
// Filesystem names stay separate from URL spellings; callers escape each
// segment when constructing resource URLs.
func ValidFrontendPath(value string) bool {
	if value == "" || len(value) > 1024 || path.Clean(value) != value {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if !validFrontendSegment(part) || part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if slices.Contains([]string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}, stem) {
			return false
		}
	}
	return true
}

func (f *Frontend) Validate() error {
	if f == nil {
		return nil
	}
	if f.Contract != FrontendFederationContract || f.APIContract != FrontendAPIContract ||
		!ValidFrontendPath(f.Root) || !ValidFrontendPath(f.Entry) ||
		path.Base(f.Entry) != "remoteEntry.js" || len(f.Exposes) == 0 || len(f.Exposes) > 64 ||
		len(f.Shared) == 0 || len(f.Shared) > 2 || !slices.Contains(f.Shared, "vue") {
		return failure("invalid_frontend_contract")
	}
	seen := map[string]bool{}
	for _, component := range f.Exposes {
		if seen[component] || !frontendComponent.MatchString(component) {
			return failure("invalid_frontend_contract")
		}
		seen[component] = true
	}
	for _, field := range []struct{ values, allowed []string }{
		{f.Shared, []string{"vue", "vuetify"}},
	} {
		seen := map[string]bool{}
		for _, value := range field.values {
			if seen[value] || !slices.Contains(field.allowed, value) {
				return failure("invalid_frontend_contract")
			}
			seen[value] = true
		}
	}
	return nil
}
