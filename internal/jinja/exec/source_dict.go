package exec

func (d *Dict) GetAttribute(name string) (*Value, bool) {
	switch name {
	case "keys", "values", "items":
		return AsValue(func(p *VarArgs) *Value {
			if err := p.Take(); err != nil {
				return ValueError(err)
			}
			out := []any{}
			for _, row := range d.Pairs {
				switch name {
				case "keys":
					out = append(out, row.Key)
				case "values":
					out = append(out, row.Value)
				case "items":
					out = append(out, Tuple{row.Key, row.Value})
				}
			}
			return AsValue(out)
		}), true
	case "get":
		return AsValue(func(p *VarArgs) *Value {
			if len(p.KwArgs) != 0 || len(p.Args) < 1 || len(p.Args) > 2 {
				return ValueError(SourceFailuref("TypeError: get takes one or two arguments"))
			}
			for _, row := range d.Pairs {
				if row.Key.EqualValueTo(p.Args[0]) {
					return row.Value
				}
			}
			if len(p.Args) == 2 {
				return p.Args[1]
			}
			return AsValue(nil)
		}), true
	}
	return AsUndefined(), false
}
