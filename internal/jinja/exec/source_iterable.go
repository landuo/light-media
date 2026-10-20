package exec

import "fmt"

// SourceIterable permits lazy, reusable Python range iteration without a
// producer goroutine that can remain blocked after a template error or break.
type SourceIterable interface {
	SourceLen() int
	SourceIterate(func(int, int, *Value, *Value) bool, func(), bool)
}
type SourceRange struct{ Start, Stop, Step int }

func (r SourceRange) SourceLen() int {
	if r.Step > 0 && r.Stop > r.Start {
		return (r.Stop-r.Start-1)/r.Step + 1
	}
	if r.Step < 0 && r.Stop < r.Start {
		return (r.Start-r.Stop-1)/(-r.Step) + 1
	}
	return 0
}
func (r SourceRange) SourceIterate(fn func(int, int, *Value, *Value) bool, empty func(), reverse bool) {
	n := r.SourceLen()
	if n == 0 {
		empty()
		return
	}
	for i := 0; i < n; i++ {
		index := i
		if reverse {
			index = n - 1 - i
		}
		if !fn(i, n, AsValue(r.Start+index*r.Step), nil) {
			return
		}
	}
}
func (r SourceRange) String() string {
	if r.Step == 1 {
		return fmt.Sprintf("range(%d, %d)", r.Start, r.Stop)
	}
	return fmt.Sprintf("range(%d, %d, %d)", r.Start, r.Stop, r.Step)
}
