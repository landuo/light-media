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

var frontendSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// ValidFrontendPath accepts only canonical, bounded package-relative paths.
// Percent escapes and URL punctuation are not file names in this contract.
func ValidFrontendPath(value string) bool {
	if value == "" || len(value) > 1024 || path.Clean(value) != value {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if !frontendSegment.MatchString(part) || part == "." || part == ".." || strings.HasSuffix(part, ".") {
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
		path.Base(f.Entry) != "remoteEntry.js" || len(f.Exposes) == 0 || len(f.Exposes) > 4 ||
		len(f.Shared) == 0 || len(f.Shared) > 2 || !slices.Contains(f.Shared, "vue") {
		return failure("invalid_frontend_contract")
	}
	for _, field := range []struct{ values, allowed []string }{
		{f.Exposes, []string{"Config", "Page", "Dashboard", "AppPage"}},
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
