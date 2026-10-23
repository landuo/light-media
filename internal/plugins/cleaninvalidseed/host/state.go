package seedhost

import "context"

// GetState reads the actual runtime configuration retained by the existing
// source machine, not a subsequently saved but unapplied configuration row.
func (o *Owner) GetState(ctx context.Context) (any, error) {
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-o.gate }()
	if len(o.runtimeConfig) == 0 {
		return false, nil
	}
	config, err := decodeConfig(o.runtimeConfig)
	if err != nil {
		return nil, err
	}
	return config["enabled"], nil
}
