// Package pythonre implements the CPython3.12 Unicode str regular-expression
// compiler and search closure consumed by the original GPW resolver.
package pythonre

// Error preserves the source exception class. Diagnostic wording is not an API.
type Error struct{ Class, Message string }

func (e *Error) Error() string { return e.Class + ": " + e.Message }
func ErrorClass(e error) string {
	if e == nil {
		return ""
	}
	if x, ok := e.(*Error); ok {
		return x.Class
	}
	return "Error"
}
func newError(class, msg string) error { return &Error{class, msg} }
func regexError(msg string) error      { return newError("error", msg) }

const (
	flagT = 1
	flagI = 2
	flagL = 4
	flagM = 8
	flagS = 16
	flagU = 32
	flagX = 64
	flagA = 256
)
const typeFlags = flagA | flagL | flagU
const maxRepeat = uint64(0xFFFFFFFF)
const maxGroups = 1073741823
const maxWidth = ^uint64(0)

type op uint8

const (
	opLiteral op = iota
	opNotLiteral
	opClass
	opAny
	opAnchor
	opBranch
	opGroup
	opRepeat
	opAssert
	opRef
	opConditional
	opAtomic
)

type classItem struct {
	kind   string
	lo, hi rune
}
type codeRange struct{ lo, hi rune }
type node struct {
	op                       op
	c                        rune
	items                    []classItem
	neg                      bool
	anchor                   string
	sub                      []*node
	alts                     [][]*node
	group, add, del, flags   int
	min, max                 uint64
	lazy, possessive, behind bool
	width                    uint64
	fold                     bool
	compiled                 []codeRange
	tail                     []classItem
}
type width struct{ lo, hi uint64 }
type Pattern struct {
	code          []*node
	flags, groups int
	minimum       uint64
}

// CPython default resource profile used by the isolated source oracle. Parser
// entry has module, caller, re.compile, re._compile, compiler.compile and parse
// active. The source interpreter can change this global limit/caller depth;
// these are resource-policy inputs, not changes to regex grammar.
const cpythonRecursionLimit = 1000
const parserEntryFrames = 6
