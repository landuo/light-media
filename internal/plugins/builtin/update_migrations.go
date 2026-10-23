package builtin

import (
	"encoding/json"
	"errors"
)

// UpdateMigration is a compiled business declaration. It grants no SQL, network,
// instance lifecycle, or worker-controlled migration authority.
type UpdateMigration struct {
	FromVersion    string
	ToVersion      string
	ValidateConfig func(json.RawMessage) error
}

// UpdateMigrationFor lists only reviewed executable conversions. Unknown native
// owner schemas must not be treated as interchangeable by a generic reinstall.
func UpdateMigrationFor(pluginID, from string) (UpdateMigration, bool) {
	if pluginID != "CategoryEditor" || from != "1.2" {
		return UpdateMigration{}, false
	}
	return UpdateMigration{FromVersion: "1.2", ToVersion: "1.3", ValidateConfig: validateCategoryUpdateConfig}, true
}

func validateCategoryUpdateConfig(raw json.RawMessage) error {
	// Both pinned upstream versions use enabled/content. Keep every byte and
	// unknown key; validate that the target executable can consume the snapshot.
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return errors.New("invalid category configuration")
	}
	var target categoryConfig
	if err := json.Unmarshal(raw, &target); err != nil {
		return err
	}
	// 1.2 leaves enabled=true after applying its file. That bit is not a new
	// authorization to replay the one-shot on upgrade. Reject without clearing it.
	if target.Enabled {
		return errors.New("category update requires explicit one-shot review")
	}
	return nil
}
