package iyuuautoseed

import (
	"light-media/internal/releasemeta/legacyanime"
	"math/big"
)

func pythonInteger(s string) (string, error) {
	n, e := legacyanime.Integer(s)
	if e != nil {
		return "", e
	}
	return n.String(), nil
}
func newBigQuotient(r *big.Rat) string { return new(big.Int).Quo(r.Num(), r.Denom()).String() }
