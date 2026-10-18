package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

// SiteReference preserves the source boundary: native Site.id is a positive
// integer; CustomSites identifiers and source sentinels are strings. A string
// containing digits never becomes the native integer with the same spelling.
type SiteReference struct {
	Native int64
	Custom string
}

func (r SiteReference) Validate() error {
	if r.Native > 0 && r.Custom == "" {
		return nil
	}
	if r.Native != 0 || r.Custom == "" || len(r.Custom) > 256 {
		return errors.New("invalid site reference")
	}
	for _, c := range r.Custom {
		if c < 32 || c == 127 {
			return errors.New("invalid site reference")
		}
	}
	return nil
}

func (r SiteReference) MarshalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.Native > 0 {
		return []byte(strconv.FormatInt(r.Native, 10)), nil
	}
	return json.Marshal(r.Custom)
}

func (r *SiteReference) UnmarshalJSON(raw []byte) error {
	*r = SiteReference{}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &r.Custom); err != nil {
			return err
		}
	} else {
		// SQLite Site.id is an integer. Reject floats/bools/null instead of
		// admitting them as a different site's identity through coercion.
		for _, b := range raw {
			if b < '0' || b > '9' {
				return errors.New("invalid native site reference")
			}
		}
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return errors.New("invalid native site reference")
		}
		r.Native = n
	}
	return r.Validate()
}

func (c SiteControls) References(key string) []SiteReference {
	var out []SiteReference
	if json.Unmarshal(c[key], &out) != nil || out == nil {
		return []SiteReference{}
	}
	return out
}

func (c SiteControls) SetReferences(key string, refs []SiteReference) {
	if refs == nil {
		refs = []SiteReference{}
	}
	c[key], _ = json.Marshal(refs)
}

func ChangeSiteReference(values []SiteReference, id SiteReference, enabled bool) ([]SiteReference, bool) {
	out := append([]SiteReference{}, values...)
	for i, ref := range out {
		if ref == id {
			if enabled {
				return out, false
			}
			return append(out[:i], out[i+1:]...), true
		}
	}
	if enabled {
		return append(out, id), true
	}
	return out, false
}
