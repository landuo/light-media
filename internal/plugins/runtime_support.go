package plugins

import "regexp"

// RuntimeSupport is descriptive build support metadata, not an RPC grant or
// proof of acceptance. Absence means undeclared, never unrestricted support.
type RuntimeSupport struct {
	Platforms    []string `json:"platforms,omitempty"`
	StorageTypes []string `json:"storage_types,omitempty"`
}

var runtimeSupportID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func (s *RuntimeSupport) Validate() error {
	if s == nil {
		return nil
	}
	for _, values := range [][]string{s.Platforms, s.StorageTypes} {
		if len(values) > 16 {
			return failure("invalid_manifest")
		}
		seen := make(map[string]bool)
		for _, value := range values {
			if !runtimeSupportID.MatchString(value) || seen[value] {
				return failure("invalid_manifest")
			}
			seen[value] = true
		}
	}
	return nil
}
