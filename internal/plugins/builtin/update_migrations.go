package builtin

import (
	"encoding/json"
	"errors"
)

// UpdateMigration is a compiled business declaration. It grants no SQL, network,
// instance lifecycle, or worker-controlled migration authority.
type UpdateMigration struct {
	FromVersion          string
	ToVersion            string
	ValidateConfig       func(json.RawMessage) error
	RequiresExplicitSave func(json.RawMessage) bool
}

// UpdateMigrationFor lists only reviewed executable conversions. Unknown native
// owner schemas must not be treated as interchangeable by a generic reinstall.
func UpdateMigrationFor(pluginID, from string) (UpdateMigration, bool) {
	if pluginID != "CategoryEditor" || from != "1.2" {
		return UpdateMigration{}, false
	}
	return UpdateMigration{FromVersion: "1.2", ToVersion: "1.3", ValidateConfig: validateCategoryUpdateConfig, RequiresExplicitSave: categoryUpdateRequiresExplicitSave}, true
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
	return nil
}

// A legacy true flag may describe an already-completed one-shot in 1.2. The
// framework preserves it but never treats it as fresh execution authorization.
func categoryUpdateRequiresExplicitSave(raw json.RawMessage) bool {
	var target categoryConfig
	return json.Unmarshal(raw, &target) == nil && target.Enabled
}
