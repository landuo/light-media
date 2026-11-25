package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const ServiceProgressCapability = "service.progress.update"
const ServiceTupleKey = "__plugin_service_tuple"

// ServiceResultTuple preserves the source distinction between a Python tuple
// (False, reason) and an ordinary list [False, reason]. JSON arrays stay lists.
func ServiceResultTuple(values ...any) (json.RawMessage, error) {
	return json.Marshal(map[string]any{ServiceTupleKey: values})
}

type ServiceProgressUpdate struct {
	Value *float64                   `json:"value,omitempty"`
	Text  *string                    `json:"text,omitempty"`
	Data  map[string]json.RawMessage `json:"data,omitempty"`
}

// UpdateServiceProgress is scoped to the currently executing scheduler method.
// No job ID is accepted; an API handler or retained caller cannot borrow it.
func UpdateServiceProgress(ctx context.Context, host HostCaller, update ServiceProgressUpdate) error {
	if host == nil {
		return errors.New("service progress requires an active host caller")
	}
	raw, err := json.Marshal(update)
	if err != nil {
		return err
	}
	_, err = host(ctx, ServiceProgressCapability, raw)
	return err
}
