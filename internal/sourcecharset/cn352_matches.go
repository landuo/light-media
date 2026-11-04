package sourcecharset

import "errors"

type cnMatches struct {
	items  []*cnMatch
	sorted bool
}

func (m *cnMatches) append(item *cnMatch) error {
	if item == nil {
		return errors.New("cannot append absent charset match")
	}
	if err := cnLoad(); err != nil {
		return err
	}
	if len(item.payload) < cnData.Big {
		for _, existing := range m.items {
			a, err := existing.text()
			if err != nil {
				return err
			}
			b, err := item.text()
			if err != nil {
				return err
			}
			// Original hashes decoded strings. Ordinary distinct decoded values are
			// not deliberately collided; equality preserves identical-value factoring
			// without a process-random Python hash dependency.
			if a == b && existing.chaos == item.chaos {
				if existing.encoding == item.encoding {
					return errors.New("identical charset cannot be its own submatch")
				}
				item.decoded = ""
				item.decodedPresent = false
				existing.leaves = append(existing.leaves, item)
				return nil
			}
		}
	}
	m.items = append(m.items, item)
	m.sorted = false
	return nil
}
func (m *cnMatches) best() (*cnMatch, error) {
	if len(m.items) == 0 {
		return nil, nil
	}
	if !m.sorted {
		if err := cnSortMatches(m.items); err != nil {
			return nil, err
		}
		m.sorted = true
	}
	return m.items[0], nil
}
func (m *cnMatches) byEncoding(name string) (*cnMatch, error) {
	for _, item := range m.items {
		if item.encoding == name {
			return item, nil
		}
		for _, leaf := range item.leaves {
			if leaf.encoding == name {
				return item, nil
			}
		}
	}
	return nil, errors.New("original charset lookup missing")
}
