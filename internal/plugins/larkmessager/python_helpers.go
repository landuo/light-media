package larkmessager

import "light-media/internal/releasemeta/legacyanime"

func legacyInteger(s string) (string, error) {
	n, e := legacyanime.Integer(s)
	if e != nil {
		return "", e
	}
	return n.String(), nil
}
