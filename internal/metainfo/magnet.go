package metainfo

import (
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxMagnetURI = 32 << 10

// MagnetHash validates one magnet URL and returns its canonical lowercase info
// hash. A hybrid URL uses btih as its identity, matching hybrid metainfo.
// The URL can carry private tracker parameters, so errors omit its contents.
func MagnetHash(uri string) (string, error) {
	query, ok := strings.CutPrefix(uri, "magnet:?")
	if !ok || len(uri) > MaxMagnetURI || !utf8.ValidString(uri) || strings.ContainsRune(query, '#') ||
		strings.ContainsFunc(query, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", invalid("invalid_magnet_uri")
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return "", invalid("invalid_magnet_uri")
	}
	for key, entries := range values {
		if strings.ContainsFunc(key, unicode.IsControl) {
			return "", invalid("invalid_magnet_uri")
		}
		for _, value := range entries {
			if strings.ContainsFunc(value, unicode.IsControl) {
				return "", invalid("invalid_magnet_uri")
			}
		}
	}
	var v1, v2 string
	for _, exact := range values["xt"] {
		exact = strings.ToLower(exact)
		if value, found := strings.CutPrefix(exact, "urn:btih:"); found {
			hash := magnetBTIH(value)
			if hash == "" || v1 != "" && hash != v1 {
				return "", invalid("invalid_magnet_hash")
			}
			v1 = hash
		}
		if value, found := strings.CutPrefix(exact, "urn:btmh:1220"); found {
			if !magnetHex(value, 64) || v2 != "" && value != v2 {
				return "", invalid("invalid_magnet_hash")
			}
			v2 = value
		}
	}
	if v1 != "" {
		return v1, nil
	}
	if v2 != "" {
		return v2, nil
	}
	return "", invalid("unsupported_magnet_hash")
}

func magnetBTIH(value string) string {
	if magnetHex(value, 40) {
		return value
	}
	if len(value) == 32 {
		if raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(value)); err == nil {
			return hex.EncodeToString(raw)
		}
	}
	return ""
}

func magnetHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
