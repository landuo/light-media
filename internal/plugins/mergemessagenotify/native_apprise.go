package mergemessagenotify

import (
	"context"
	"errors"

	"light-media/internal/plugins/mergemessagenotify/apprise"
)

// NativeApprise binds the source channel to its Go dependency. The operation
// owner supplies an authorized transport; there is no implicit network client.
// Valid unported providers/attachments remain an explicit native dependency.
func NativeApprise(transport apprise.Transport) func(context.Context, AppriseRequest) (any, error) {
	return func(ctx context.Context, input AppriseRequest) (any, error) {
		client := apprise.New(transport)
		for _, raw := range input.URLs {
			if _, err := client.Add(raw); err != nil {
				if errors.Is(err, apprise.ErrUnsupported) {
					return nil, ErrDependency
				}
				return nil, err
			}
		}
		value, err := client.Notify(ctx, input.Title, input.Body, input.Attach)
		if errors.Is(err, apprise.ErrUnsupported) {
			return nil, ErrDependency
		}
		return value, err
	}
}
