package brushflowlanduo

import (
	"bytes"
	"encoding/json"
	"sort"
)

// RecordSet retains Python dict insertion order across source KV round trips.
// Replacing a record does not move it; deleting and reinserting does.
type RecordSet struct {
	Order   []string
	Values  Rows
	rawRows map[string]json.RawMessage
}

func NewRecords() *RecordSet { return &RecordSet{Order: []string{}, Values: Rows{}} }
func (r *RecordSet) Set(k string, v Row) {
	if _, ok := r.Values[k]; !ok {
		r.Order = append(r.Order, k)
	}
	r.Values[k] = v
	delete(r.rawRows, k)
}
func (r *RecordSet) Del(k string) Row {
	v, ok := r.Values[k]
	if !ok {
		return nil
	}
	delete(r.Values, k)
	delete(r.rawRows, k)
	for i, x := range r.Order {
		if x == k {
			r.Order = append(r.Order[:i], r.Order[i+1:]...)
			break
		}
	}
	return v
}
func (r *RecordSet) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range r.Order {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		v := []byte(r.rawRows[k])
		if v == nil {
			var err error
			v, err = json.Marshal(r.Values[k])
			if err != nil {
				return nil, err
			}
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func (r *RecordSet) UnmarshalJSON(b []byte) error {
	return r.unmarshalJSON(b, false)
}

func (r *RecordSet) unmarshalJSON(b []byte, keepNonRows bool) error {
	r.Order = []string{}
	r.Values = Rows{}
	r.rawRows = nil
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	tok, e := d.Token()
	if e != nil {
		return e
	}
	if tok == nil {
		return nil
	}
	if tok != json.Delim('{') {
		return configError("records must be an object")
	}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return e
		}
		var v Row
		key := t.(string)
		if keepNonRows {
			// The source's upgrade pass skips non-dict entries and preserves
			// them in the same dictionary when other rows are changed.
			var raw json.RawMessage
			if e = d.Decode(&raw); e != nil {
				return e
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if decoder.Decode(&v) != nil {
				r.Set(key, nil)
				if r.rawRows == nil {
					r.rawRows = map[string]json.RawMessage{}
				}
				r.rawRows[key] = raw
				continue
			}
		} else if e = d.Decode(&v); e != nil {
			return e
		}
		r.Set(key, v)
	}
	_, e = d.Token()
	return e
}
func recordSet(v any) (*RecordSet, error) {
	return decodeRecordSet(v, false)
}

func decodeRecordSet(v any, keepNonRows bool) (*RecordSet, error) {
	if v == nil {
		return NewRecords(), nil
	}
	if r, ok := v.(*RecordSet); ok {
		return r, nil
	}
	var b []byte
	var e error
	switch x := v.(type) {
	case json.RawMessage:
		b = x
	case []byte:
		b = x
	default:
		b, e = json.Marshal(v)
	}
	if e != nil {
		return nil, e
	}
	r := NewRecords()
	e = r.unmarshalJSON(b, keepNonRows)
	return r, e
}
func recordsFromRows(v Rows) *RecordSet {
	r := NewRecords()
	ks := keys(v)
	sort.Strings(ks)
	for _, k := range ks {
		r.Set(k, v[k])
	}
	return r
}
