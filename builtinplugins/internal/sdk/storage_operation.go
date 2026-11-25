package plugins

import "context"

// StorageOperation is the local StorageBase object returned by the ordinary
// storage.operation event. Each method is a declared method on this instance's
// live SDK object. Key distinguishes multiple storage objects owned by it.
// This descriptor is scoped to the current transfer, never persisted.
type StorageOperation struct {
	Kind       string            `json:"kind"`
	InstanceID string            `json:"instance_id"`
	Key        string            `json:"key"`
	Methods    map[string]string `json:"methods"`
}

func (b Base) LocalStorageOperation(ctx context.Context, key string, methods map[string]string) (StorageOperation, error) {
	identity, err := b.GetInstanceMetadata(ctx)
	if err != nil {
		return StorageOperation{}, err
	}
	return StorageOperation{Kind: "local-storage", InstanceID: identity.InstanceID, Key: key, Methods: methods}, nil
}

// Path is the logical target passed to StorageBase. PublicationPath, when
// present, is the caller-authorized staging file the native publisher will
// verify and atomically publish there. It preserves durable host publication
// without exposing a host filesystem handle or granting another path.
type StorageOperationCall struct {
	Key             string          `json:"key"`
	Operation       string          `json:"operation"`
	FileItem        *ModuleFileItem `json:"fileitem"`
	Path            string          `json:"path"`
	NewName         *string         `json:"new_name"`
	PublicationPath string          `json:"publication_path,omitempty"`
}
