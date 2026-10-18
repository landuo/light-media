package sites

import "context"

// AddRequestGuard composes an owner check without dropping the application's
// rate, budget, cooldown and live-configuration checks already installed.
func (c *Client) AddRequestGuard(guard func(context.Context) error) {
	prior := c.requestGuard
	c.requestGuard = func(ctx context.Context) error {
		if prior != nil {
			if e := prior(ctx); e != nil {
				return e
			}
		}
		if guard != nil {
			return guard(ctx)
		}
		return nil
	}
}
