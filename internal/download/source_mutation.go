package download

import (
	"context"
	"net/http"
	"net/url"
)

// These helpers are private to the fixed post-Add operations. They preserve
// actual preflight/rejection proofs rather than inspecting a public error code
// after the call. Only a definite authentication rejection permits another
// attempt; transport failures, 5xx and malformed RPC responses are never retried.
func (c *Client) sourceQBMutation(ctx context.Context, endpoint string, args url.Values) error {
	if !c.loggedIn && (c.config.Username != "" || c.config.Password != "") {
		if err := c.qbLogin(ctx); err != nil {
			return beforeMutation(err)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			if attempt == 0 {
				return beforeMutation(err)
			}
			return rejectedMutation(err) // the previous attempt was auth-rejected.
		}
		_, _, status, err := c.request(ctx, http.MethodPost, c.qbURL(endpoint), []byte(args.Encode()), "application/x-www-form-urlencoded", "")
		if err != nil {
			return err
		}
		if status == http.StatusForbidden || status == http.StatusUnauthorized {
			if c.config.APIKey != "" || attempt != 0 {
				return rejectedMutation(statusError(status))
			}
			c.loggedIn = false
			if err := c.qbLogin(ctx); err != nil {
				return rejectedMutation(err)
			}
			continue
		}
		if status != http.StatusOK {
			return statusError(status)
		}
		return nil
	}
	return rejectedMutation(failure("source_post_add", "authentication_rejected"))
}

func (c *Client) sourceTRMutation(ctx context.Context, method string, args map[string]any) error {
	if err := ctx.Err(); err != nil {
		return beforeMutation(err)
	}
	_, err := c.trRPC(ctx, method, args, c.trDialect)
	if typed, ok := err.(*Error); ok && typed.Operation == "rpc" && typed.Code == "rpc_rejected" {
		return rejectedMutation(err)
	}
	return err
}
