package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"
)

// BuiltinDescriptor represents an ordered tree of the six fixed APScheduler
// builtin objects. It is not the APS string-alias constructor, a Python class
// reference, a durable pickled job, or authority to schedule an execution.
// Existing Spec/New/Trigger callers retain their existing three-kind API.
type BuiltinDescriptor struct {
	Kind       string                `json:"kind"`
	Timezone   string                `json:"timezone,omitempty"`
	Cron       string                `json:"cron,omitempty"`
	Fields     *CronFields           `json:"fields,omitempty"`
	IntervalMS int64                 `json:"interval_ms,omitempty"`
	RunAt      string                `json:"run_at,omitempty"`
	StartAt    string                `json:"start_at,omitempty"`
	EndAt      string                `json:"end_at,omitempty"`
	Calendar   *CalendarIntervalSpec `json:"calendar,omitempty"`
	Children   []BuiltinDescriptor   `json:"children,omitempty"`
	// nil preserves omitted/None; an explicit zero matters to Interval's RNG
	// call, unlike BaseTrigger._apply_jitter which skips both None and zero.
	Jitter *float64 `json:"jitter,omitempty"`
}

func (d *BuiltinDescriptor) UnmarshalJSON(raw []byte) error {
	type plain BuiltinDescriptor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var next plain
	if err := dec.Decode(&next); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errTrigger
	}
	if next.Kind == "" {
		return errTrigger
	}
	*d = BuiltinDescriptor(next)
	return nil
}

// These are explicit candidate policy inputs, not hidden production defaults.
// The host's adoption and fault handling require their own approved policy.
// Limits bound construction and the entire recursive evaluation, not each child
// independently. Exhaustion is an error and can never become source None.
type BuiltinLimits struct {
	MaxNodes       int
	MaxDepth       int
	MaxEvaluations int
}

type BuiltinBoundaryError struct{ Code string }

func (e *BuiltinBoundaryError) Error() string { return "builtin trigger boundary: " + e.Code }
func builtinBoundary(code string) error       { return &BuiltinBoundaryError{Code: code} }

type BuiltinSourceError struct{ Class, Message string }

func (e *BuiltinSourceError) Error() string { return e.Class + ": " + e.Message }

func cloneBuiltinDescriptor(d BuiltinDescriptor) BuiltinDescriptor {
	if d.Fields != nil {
		v := *d.Fields
		d.Fields = &v
	}
	if d.Calendar != nil {
		v := cloneCalendarSpec(*d.Calendar)
		d.Calendar = &v
	}
	if d.Jitter != nil {
		v := *d.Jitter
		d.Jitter = &v
	}
	if d.Children != nil {
		children := make([]BuiltinDescriptor, len(d.Children))
		for i := range children {
			children[i] = cloneBuiltinDescriptor(d.Children[i])
		}
		d.Children = children
	}
	return d
}

// BuiltinTrigger is immutable after construction. Each Next invocation owns
// its recursion counters, previous/now arguments and observation ports.
type BuiltinTrigger struct {
	descriptor BuiltinDescriptor
	root       *builtinNode
	limits     BuiltinLimits
	locations  map[string]*time.Location
}

type builtinNode struct {
	descriptor        BuiltinDescriptor
	children          []*builtinNode
	location          *time.Location
	cron              *Cron
	calendar          *calendarInterval
	start, end, runAt *time.Time
}

func NewBuiltin(d BuiltinDescriptor, anchor time.Time, limits BuiltinLimits) (*BuiltinTrigger, error) {
	if anchor.Year() < 1 || anchor.Year() > 9999 {
		return nil, errTrigger
	}
	return newBuiltinWithClock(&d, "", limits, builtinConstructorClock{
		now: func(location *time.Location) time.Time { return anchor.In(location) },
		localDate: func() time.Time {
			y, m, day := anchor.In(time.Local).Date()
			return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
		},
	})
}

func newBuiltinWithClock(d *BuiltinDescriptor, schedulerTimezone string, limits BuiltinLimits, clock builtinConstructorClock) (*BuiltinTrigger, error) {
	if limits.MaxNodes <= 0 || limits.MaxDepth <= 0 || limits.MaxEvaluations <= 0 {
		return nil, builtinBoundary("explicit_evaluation_policy_required")
	}
	if clock.now == nil || clock.localDate == nil {
		return nil, builtinBoundary("invalid_constructor_clock")
	}
	if d == nil {
		d = &BuiltinDescriptor{Kind: "date", Timezone: schedulerTimezone}
	}
	count := 0
	locations := map[string]*time.Location{}
	var build func(BuiltinDescriptor, int) (*builtinNode, error)
	build = func(s BuiltinDescriptor, depth int) (*builtinNode, error) {
		count++
		if count > limits.MaxNodes {
			return nil, builtinBoundary("descriptor_nodes")
		}
		if depth > limits.MaxDepth {
			return nil, builtinBoundary("descriptor_depth")
		}
		if s.Jitter != nil && (math.IsNaN(*s.Jitter) || math.IsInf(*s.Jitter, 0) || math.Abs(*s.Jitter) > float64(math.MaxInt64)/float64(time.Second)) {
			return nil, errTrigger
		}
		n := &builtinNode{descriptor: s}
		switch s.Kind {
		case "and", "or":
			if s.Timezone != "" || s.Cron != "" || s.Fields != nil || s.IntervalMS != 0 || s.RunAt != "" || s.StartAt != "" || s.EndAt != "" || s.Calendar != nil {
				return nil, errTrigger
			}
			n.children = make([]*builtinNode, len(s.Children))
			for i, child := range s.Children {
				var err error
				n.children[i], err = build(child, depth+1)
				if err != nil {
					return nil, err
				}
			}
		case "cron", "interval", "date", "calendarinterval":
			if s.Children != nil {
				return nil, errTrigger
			}
			if err := compileBuiltinLeaf(n, clock, locations); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported builtin trigger kind %q", s.Kind)
		}
		// Clone only after the bounded traversal. Do not recurse over unvalidated
		// caller memory first, and do not retain caller-owned child/field pointers.
		n.descriptor.Children = nil
		if s.Kind == "and" || s.Kind == "or" {
			n.descriptor.Children = make([]BuiltinDescriptor, len(n.children))
			for i, child := range n.children {
				n.descriptor.Children[i] = child.descriptor
			}
		}
		n.descriptor = cloneBuiltinDescriptor(n.descriptor)
		return n, nil
	}
	root, err := build(*d, 1)
	if err != nil {
		return nil, err
	}
	return &BuiltinTrigger{descriptor: cloneBuiltinDescriptor(root.descriptor), root: root, limits: limits, locations: locations}, nil
}
func (t *BuiltinTrigger) Descriptor() BuiltinDescriptor { return cloneBuiltinDescriptor(t.descriptor) }
