package brushflow

import (
	"bytes"
	"encoding/json"
	"sort"
)

// RecordSet retains Python dict insertion order across source KV round trips.
// Replacing a record does not move it; deleting and reinserting does.
type RecordSet struct {
	Order  []string
	Values Rows
}

func NewRecords() *RecordSet { return &RecordSet{Order: []string{}, Values: Rows{}} }
func (r *RecordSet) Set(k string, v Row) {
	if _, ok := r.Values[k]; !ok {
		r.Order = append(r.Order, k)
	}
	r.Values[k] = v
}
func (r *RecordSet) Del(k string) Row {
	v, ok := r.Values[k]
	if !ok {
		return nil
	}
	delete(r.Values, k)
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
		v, e := json.Marshal(r.Values[k])
		if e != nil {
			return nil, e
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func (r *RecordSet) UnmarshalJSON(b []byte) error {
	r.Order = []string{}
	r.Values = Rows{}
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
		if e = d.Decode(&v); e != nil {
			return e
		}
		r.Set(t.(string), v)
	}
	_, e = d.Token()
	return e
}
func recordSet(v any) (*RecordSet, error) {
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
	e = r.UnmarshalJSON(b)
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
