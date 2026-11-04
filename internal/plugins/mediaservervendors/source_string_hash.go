package mediaservervendors

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
	"sync"
)

// This is the fixed CPython 3.12.14, little-endian, 64-bit SipHash13
// string-hash profile. It is not a hash implementation for arbitrary objects.
type sourceStringHashSecret struct{ k0, k1 uint64 }

var sourceStringProcessSecret struct {
	once   sync.Once
	secret sourceStringHashSecret
	err    error
}

func sourceStringDefaultSecret() (sourceStringHashSecret, error) {
	sourceStringProcessSecret.once.Do(func() {
		// CPython initializes a 24-byte union once per process; string
		// SipHash uses its first 16 bytes. Never substitute a fixed seed if
		// entropy fails, and never generate a different secret per set.
		var secret [24]byte
		if _, err := io.ReadFull(rand.Reader, secret[:]); err != nil {
			sourceStringProcessSecret.err = err
			return
		}
		sourceStringProcessSecret.secret = sourceStringSecretBytes(secret)
	})
	return sourceStringProcessSecret.secret, sourceStringProcessSecret.err
}

func sourceStringSecretBytes(secret [24]byte) sourceStringHashSecret {
	return sourceStringHashSecret{
		k0: binary.LittleEndian.Uint64(secret[0:8]),
		k1: binary.LittleEndian.Uint64(secret[8:16]),
	}
}

func sourceStringSeedSecret(seed uint32) sourceStringHashSecret {
	var secret [24]byte
	// bootstrap_hash.c: seed zero disables randomization (all zero bytes).
	if seed != 0 {
		x := seed
		for i := range secret {
			x = x*214013 + 2531011 // uint32 overflow is the original modulo.
			secret[i] = byte(x >> 16)
		}
	}
	return sourceStringSecretBytes(secret)
}

func sourceStringHash(points []rune, secret sourceStringHashSecret) (int64, error) {
	var maximum rune
	for _, p := range points {
		if p < 0 || p > 0x10ffff {
			return 0, errors.New("source string set: code point outside Python Unicode range")
		}
		if p > maximum {
			maximum = p
		}
	}
	if len(points) == 0 {
		return 0, nil
	}
	kind := 1
	if maximum > 0xff {
		kind = 2
	}
	if maximum > 0xffff {
		kind = 4
	}
	maxInt := int(^uint(0) >> 1)
	if len(points) > maxInt/kind {
		return 0, errors.New("source string set: Unicode storage length overflow")
	}
	// PEP 393 canonical kind, not UTF-8 or UTF-16 surrogate pairing.
	// A lone surrogate is a real Python code point and remains two bytes.
	data := make([]byte, len(points)*kind)
	for i, p := range points {
		switch kind {
		case 1:
			data[i] = byte(p)
		case 2:
			binary.LittleEndian.PutUint16(data[i*2:], uint16(p))
		case 4:
			binary.LittleEndian.PutUint32(data[i*4:], uint32(p))
		}
	}
	hash := int64(sourceStringSipHash13(data, secret.k0, secret.k1))
	if hash == -1 {
		hash = -2
	}
	return hash, nil
}

func sourceStringSipHash13(data []byte, k0, k1 uint64) uint64 {
	v0 := k0 ^ uint64(0x736f6d6570736575)
	v1 := k1 ^ uint64(0x646f72616e646f6d)
	v2 := k0 ^ uint64(0x6c7967656e657261)
	v3 := k1 ^ uint64(0x7465646279746573)
	round := func() {
		v0 += v1
		v2 += v3
		v1 = bits.RotateLeft64(v1, 13) ^ v0
		v3 = bits.RotateLeft64(v3, 16) ^ v2
		v0 = bits.RotateLeft64(v0, 32)
		v2 += v1
		v0 += v3
		v1 = bits.RotateLeft64(v1, 17) ^ v2
		v3 = bits.RotateLeft64(v3, 21) ^ v0
		v2 = bits.RotateLeft64(v2, 32)
	}
	tail := uint64(len(data)) << 56
	for len(data) >= 8 {
		word := binary.LittleEndian.Uint64(data[:8])
		v3 ^= word
		round()
		v0 ^= word
		data = data[8:]
	}
	for i, b := range data {
		tail |= uint64(b) << (8 * i)
	}
	v3 ^= tail
	round()
	v0 ^= tail
	v2 ^= 0xff
	round()
	round()
	round()
	return (v0 ^ v1) ^ (v2 ^ v3)
}
