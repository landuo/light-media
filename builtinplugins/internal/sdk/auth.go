package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const AuthTicketCreateCapability = "auth.ticket.create"

// CreateAuthTicket is the Go host equivalent of create_plugin_auth_ticket.
// The plugin must authenticate the external proof before asserting a local
// user. Call only inside an active concurrent worker invocation, without a
// lock needed by get_state/get_auth_providers: the host reads both afresh.
func CreateAuthTicket(ctx context.Context, host HostCaller, userID int64, providerID string, metadata json.RawMessage) (string, error) {
	if host == nil {
		return "", errors.New("authentication requires an active host caller")
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(struct {
		UserID     int64           `json:"user_id"`
		ProviderID string          `json:"provider_id"`
		Metadata   json.RawMessage `json:"metadata"`
	}{userID, providerID, metadata})
	if err != nil {
		return "", err
	}
	result, err := host(ctx, AuthTicketCreateCapability, raw)
	if err != nil {
		return "", err
	}
	var out struct {
		Ticket string `json:"ticket"`
	}
	if json.Unmarshal(result, &out) != nil || out.Ticket == "" {
		return "", errors.New("invalid authentication ticket response")
	}
	return out.Ticket, nil
}
