package sitestatistic

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sort"
)

// SourceAccounts supplies source model column order. Keep it while decoding
// so __sub_data raises at the same first shared numeric field. This private
// marker is never serialized or included in source to_dict iteration.
const rowOrderKey = "\x00site-statistic-column-order"

func (d *Data) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	first, e := decoder.Token()
	if e != nil {
		return e
	}
	if first == nil {
		*d = nil
		return nil
	}
	if first != json.Delim('{') {
		return &SourceError{"TypeError", "source account row is not an object"}
	}
	row := Data{}
	order := []string{}
	for decoder.More() {
		token, e := decoder.Token()
		if e != nil {
			return e
		}
		key := token.(string)
		var value any
		if e = decoder.Decode(&value); e != nil {
			return e
		}
		if _, exists := row[key]; !exists {
			order = append(order, key)
		}
		row[key] = value
	}
	if _, e = decoder.Token(); e != nil {
		return e
	}
	row[rowOrderKey] = order
	*d = row
	return nil
}
func (d Data) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	var out bytes.Buffer
	out.WriteByte('{')
	keys := dataFields(d)
	for i, k := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		value, e := json.Marshal(d[k])
		if e != nil {
			return nil, e
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
func dataFields(d Data) []string {
	if keys, ok := d[rowOrderKey].([]string); ok {
		return keys
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, key := range characterProfile.Columns {
		if _, exists := d[key]; exists {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	extra := []string{}
	for key := range d {
		if key != rowOrderKey && !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return append(keys, extra...)
}
func subtraction(d1, d2 Data) map[string]*big.Int {
	result := map[string]*big.Int{}
	if len(d1) == 0 || len(d2) == 0 {
		return result
	}
	for _, key := range dataFields(d1) {
		other, exists := d2[key]
		if !exists || !subDigit(d1[key]) || !subDigit(other) {
			continue
		}
		if key == "upload" || key == "download" {
			if subInteger(other).Sign() <= 0 {
				continue
			}
		}
		value := subInteger(d1[key])
		value.Sub(value, subInteger(other))
		result[key] = value
	}
	return result
}
