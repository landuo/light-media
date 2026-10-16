package durable

import (
	"crypto/rand"
	"errors"
	"math/big"
)

func defaultChoose(minimum, maximum int) (int, error) {
	if maximum < minimum {
		return 0, errors.New("empty range in randrange(0, 0)")
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(maximum-minimum+1)))
	if err != nil {
		return 0, err
	}
	return minimum + int(v.Int64()), nil
}
