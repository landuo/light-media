package mediaservervendors

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
)

// The prefix and API key are public protocol identifiers, never user secrets.
// Clock/random observations are explicit so deterministic original traces do
// not become a fake wall clock or a claim about Python PRNG state.
func trimeAuthX(apiPath, payload, apiKey string, nonce int, seconds float64) (string, error) {
	if nonce < 100000 || nonce > 999999 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds*1000 >= math.Exp2(63) || seconds*1000 < -math.Exp2(63) {
		return "", errors.New("Trime signature clock/random observation outside typed domain")
	}
	if !strings.HasPrefix(apiPath, "/v") {
		apiPath = "/v" + apiPath
	}
	n := strconv.Itoa(nonce)
	timestamp := strconv.FormatInt(int64(seconds*1000), 10)
	body := md5.Sum([]byte(payload))
	bodyHash := hex.EncodeToString(body[:])
	digest := md5.Sum([]byte(strings.Join([]string{"NDzZTVxnRKP8Z0jXg1VAMonaG8akvh", apiPath, n, timestamp, bodyHash, apiKey}, "_")))
	return "nonce=" + n + "&timestamp=" + timestamp + "&sign=" + hex.EncodeToString(digest[:]), nil
}
