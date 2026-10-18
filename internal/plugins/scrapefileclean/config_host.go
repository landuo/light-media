package scrapefileclean

import "encoding/json"

// Apply exposes the existing source configuration parser to its trusted native
// owner. It performs the same ordered assignment as Machine.Init without
// starting watchers, rebuilding state or executing a pending deletion.
func (c *Config) Apply(raw json.RawMessage) (err error) { defer capture(&err); return c.apply(raw) }

// DecodeConfiguration keeps Python truth and numeric presence available to the
// owner without widening the SDK's fixed capability payloads.
func DecodeConfiguration(raw json.RawMessage) (any, error) { return decode(raw) }
