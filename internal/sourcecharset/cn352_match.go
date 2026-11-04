package sourcecharset

import (
	"errors"
	"math"
	"strings"
)

type cnCoherence struct {
	Language string
	Ratio    float64
}

// Payload is owned once by the eventual detection invocation. Candidate states
// share that immutable slice; no globals retain input or decoded text.
type cnMatch struct {
	payload        []byte
	encoding       string
	chaos          float64
	bom            bool
	languages      []cnCoherence
	decoded        string
	decodedPresent bool
	leaves         []*cnMatch
}

func cnNewMatch(payload []byte, name string, chaos float64, bom bool, languages []cnCoherence, decoded *string) *cnMatch {
	m := &cnMatch{payload: payload, encoding: name, chaos: chaos, bom: bom, languages: append([]cnCoherence(nil), languages...)}
	if decoded != nil {
		m.decoded = *decoded
		m.decodedPresent = true
	}
	return m
}
func (m *cnMatch) text() (string, error) {
	if m == nil {
		return "", errors.New("missing charset match")
	}
	if !m.decodedPresent {
		text, err := cnDecode(m.payload, m.encoding, "strict")
		if err != nil {
			return "", err
		}
		if m.bom && m.encoding == "utf_7" {
			text = strings.TrimPrefix(text, "\ufeff")
		}
		m.decoded = text
		m.decodedPresent = true
	}
	return m.decoded, nil
}
func (m *cnMatch) coherence() float64 {
	if len(m.languages) == 0 {
		return 0
	}
	return m.languages[0].Ratio
}
func (m *cnMatch) multibyteUsage() (float64, error) {
	if len(m.payload) == 0 {
		return 0, nil
	}
	text, err := m.text()
	if err != nil {
		return 0, err
	}
	points, err := cnCodepoints(text)
	if err != nil {
		return 0, err
	}
	return 1 - float64(len(points))/float64(len(m.payload)), nil
}

// Exact original comparison branches. Sorting itself is a separate remaining
// algorithm: do not silently substitute Go sort for Python's stable sort when
// this tolerance comparator may be non-transitive.
func (m *cnMatch) less(other *cnMatch) (bool, error) {
	if m == nil || other == nil {
		return false, errors.New("missing charset comparison operand")
	}
	chaosDifference := math.Abs(m.chaos - other.chaos)
	coherenceDifference := math.Abs(m.coherence() - other.coherence())
	if chaosDifference < 0.005 && coherenceDifference > 0.02 {
		return m.coherence() > other.coherence(), nil
	}
	if chaosDifference < 0.005 && coherenceDifference <= 0.02 {
		if err := cnLoad(); err != nil {
			return false, err
		}
		if len(m.payload) >= cnData.Big {
			return m.chaos < other.chaos, nil
		}
		a, err := m.multibyteUsage()
		if err != nil {
			return false, err
		}
		b, err := other.multibyteUsage()
		return a > b, err
	}
	return m.chaos < other.chaos, nil
}
