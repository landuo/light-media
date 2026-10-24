package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateTransferSourceOverlay(root, overlay string) error {
	raw, err := os.ReadFile(overlay)
	if err != nil {
		return fmt.Errorf("Transfer fixture overlay read: %w", err)
	}
	var config struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("Transfer fixture overlay parse: %w", err)
	}
	if len(config.Replace) == 0 {
		return fmt.Errorf("Transfer fixture overlay must contain nonempty Replace")
	}
	for target, replacement := range config.Replace {
		if !filepath.IsAbs(target) || !filepath.IsAbs(replacement) {
			return fmt.Errorf("Transfer fixture overlay requires absolute targets and replacements")
		}
		target = filepath.Clean(target)
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Transfer fixture overlay target is outside selected module")
		}
		// Merely checking containment is insufficient: a frozen checkout may
		// itself be nested under the ordinary tree's work/ directory. Reject
		// a target belonging to that other module instead of silently building
		// the wrong tree. Virtual new files need no physical parent directory.
		for dir := filepath.Dir(target); dir != root; dir = filepath.Dir(dir) {
			_, err := os.Stat(filepath.Join(dir, "go.mod"))
			if err == nil {
				return fmt.Errorf("Transfer fixture overlay target belongs to a different nested module")
			}
			if !os.IsNotExist(err) {
				return fmt.Errorf("Transfer fixture overlay module check: %w", err)
			}
		}
	}
	return nil
}
