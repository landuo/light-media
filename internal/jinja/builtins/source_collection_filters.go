package builtins

import (
	"unicode"

	"light-media/internal/jinja/exec"
)

func init() {
	for _, name := range []string{"select", "reject"} {
		if err := Filters.Replace(name, func(e *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
			out := []any{}
			var fail *exec.Value
			in.Iterate(func(_, _ int, v, _ *exec.Value) bool {
				keep := v.IsTrue()
				if len(p.Args) > 0 {
					test := p.Args[0].String()
					args := exec.NewVarArgs()
					args.Args = p.Args[1:]
					args.KwArgs = p.KwArgs
					value := e.ExecuteTestByName(test, v, args)
					if value.IsError() {
						fail = value
						return false
					}
					keep = value.IsTrue()
				}
				if name == "reject" {
					keep = !keep
				}
				if keep {
					out = append(out, v)
				}
				return true
			}, func() {})
			if fail != nil {
				return fail
			}
			return exec.AsValue(out)
		}); err != nil {
			panic(err)
		}
	}
	oldSort, _ := Filters.Get("sort")
	if err := Filters.Replace("sort", func(e *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
		if in.IsIterable() {
			kind := ""
			count := 0
			valid := true
			in.Iterate(func(_, _ int, v, _ *exec.Value) bool {
				count++
				k := "other"
				if v.IsString() {
					k = "str"
				} else if v.IsNumber() || v.IsBool() {
					k = "num"
				}
				if kind == "" {
					kind = k
				} else if k != kind || k == "other" {
					valid = false
				}
				return true
			}, func() {})
			if count > 1 && !valid {
				return exec.ValueError(exec.SourceFailuref("TypeError: values cannot be ordered"))
			}
		}
		return oldSort(e, in, p)
	}); err != nil {
		panic(err)
	}
	oldReverse, _ := Filters.Get("reverse")
	if err := Filters.Replace("reverse", func(e *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
		if in.IsString() {
			if err := p.Take(); err != nil {
				return exec.ValueError(err)
			}
			r := []rune(in.String())
			for i := 0; i < len(r)/2; i++ {
				r[i], r[len(r)-1-i] = r[len(r)-1-i], r[i]
			}
			return exec.AsValue(string(r))
		}
		return oldReverse(e, in, p)
	}); err != nil {
		panic(err)
	}
	if err := Filters.Replace("wordcount", func(_ *exec.Evaluator, in *exec.Value, p *exec.VarArgs) *exec.Value {
		if in.IsError() {
			return in
		}
		if err := p.Take(); err != nil {
			return exec.ValueError(err)
		}
		count := 0
		last := false
		for _, r := range in.String() {
			word := unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_'
			if word && !last {
				count++
			}
			last = word
		}
		return exec.AsValue(count)
	}); err != nil {
		panic(err)
	}
}
