package mediaservervendors

import (
	"errors"
	"unicode/utf8"
)

// SourceStringSet is a call-owned CPython 3.12.14 add-only string set.
// It preserves table-slot iteration, including resize reinsertion order.
// It is not safe for concurrent mutation; consumers finish Add before Values.
// It deliberately has no delete/update/pop or arbitrary-object entry points.
type SourceStringSet struct {
	secret sourceStringHashSecret
	table  []sourceStringSetEntry
	used   int
}

type sourceStringSetEntry struct {
	occupied bool
	hash     int64
	points   []rune
}

// NewSourceStringSet uses the shared process-random hash secret. A failure to
// obtain entropy is returned, never converted to seed zero or insertion order.
func NewSourceStringSet() (*SourceStringSet, error) {
	secret, err := sourceStringDefaultSecret()
	if err != nil {
		return nil, err
	}
	return sourceStringNewSet(secret), nil
}

// NewSourceStringSetWithSeed represents an explicitly selected CPython
// PYTHONHASHSEED profile. It does not mutate the process default. Production
// consumers use NewSourceStringSet; source-oracle tests select their real seed.
func NewSourceStringSetWithSeed(seed uint32) *SourceStringSet {
	return sourceStringNewSet(sourceStringSeedSecret(seed))
}

func sourceStringNewSet(secret sourceStringHashSecret) *SourceStringSet {
	return &SourceStringSet{secret: secret, table: make([]sourceStringSetEntry, 8)}
}

// Add accepts a genuine UTF-8 string without normalization. Invalid UTF-8 is
// not silently replaced. Use AddCodepoints for Python lone-surrogate strings.
func (s *SourceStringSet) Add(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("source string set: invalid UTF-8; use exact code points")
	}
	return s.AddCodepoints([]rune(value))
}

// AddCodepoints accepts the exact Python str domain, including lone
// surrogates. It copies retained input; callers cannot mutate a stored key.
func (s *SourceStringSet) AddCodepoints(points []rune) error {
	if s == nil || len(s.table) == 0 {
		return errors.New("source string set: constructor required")
	}
	hash, err := sourceStringHash(points, s.secret)
	if err != nil {
		return err
	}
	mask := uint64(len(s.table) - 1)
	perturb := uint64(hash)
	index := perturb & mask
	for {
		probes := uint64(0)
		if index+9 <= mask {
			probes = 9
		}
		for offset := uint64(0); offset <= probes; offset++ {
			entry := &s.table[index+offset]
			if !entry.occupied {
				entry.occupied = true
				entry.hash = hash
				entry.points = append([]rune(nil), points...)
				s.used++
				// No deletion means fill == used at every point.
				if uint64(s.used)*5 < mask*3 {
					return nil
				}
				factor := 4
				if s.used > 50000 {
					factor = 2
				}
				maxInt := int(^uint(0) >> 1)
				if s.used > maxInt/factor {
					return errors.New("source string set: table length overflow")
				}
				return s.resize(s.used * factor)
			}
			if entry.hash == hash && sourceStringPointsEqual(entry.points, points) {
				return nil
			}
		}
		perturb >>= 5
		index = (index*5 + 1 + perturb) & mask
	}
}

func sourceStringPointsEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *SourceStringSet) resize(minused int) error {
	size := 8
	maxInt := int(^uint(0) >> 1)
	for size <= minused {
		if size > maxInt/2 {
			return errors.New("source string set: table length overflow")
		}
		size <<= 1
	}
	table := make([]sourceStringSetEntry, size)
	mask := uint64(size - 1)
	// The old table is scanned in slot order, NOT original insertion order.
	for _, entry := range s.table {
		if entry.occupied {
			sourceStringInsertClean(table, mask, entry)
		}
	}
	s.table = table
	return nil
}

func sourceStringInsertClean(table []sourceStringSetEntry, mask uint64, value sourceStringSetEntry) {
	perturb := uint64(value.hash)
	index := perturb & mask
	for {
		probes := uint64(0)
		if index+9 <= mask {
			probes = 9
		}
		for offset := uint64(0); offset <= probes; offset++ {
			if !table[index+offset].occupied {
				table[index+offset] = value
				return
			}
		}
		perturb >>= 5
		index = (index*5 + 1 + perturb) & mask
	}
}

// ValuesCodepoints snapshots ascending table slots, preserving every code
// point. Iteration and future mutation do not share returned backing arrays.
func (s *SourceStringSet) ValuesCodepoints() [][]rune {
	if s == nil {
		return nil
	}
	result := make([][]rune, 0, s.used)
	for _, entry := range s.table {
		if entry.occupied {
			points := make([]rune, len(entry.points))
			copy(points, entry.points)
			result = append(result, points)
		}
	}
	return result
}

// Values is the ordinary UTF-8 carrier. A Python lone surrogate cannot be
// represented as valid Go UTF-8: report it rather than replacing it by U+FFFD.
func (s *SourceStringSet) Values() ([]string, error) {
	values := s.ValuesCodepoints()
	result := make([]string, 0, len(values))
	for _, points := range values {
		for _, p := range points {
			if p >= 0xd800 && p <= 0xdfff {
				return nil, errors.New("source string set: result requires code-point carrier")
			}
		}
		result = append(result, string(points))
	}
	return result, nil
}
