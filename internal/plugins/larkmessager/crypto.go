package larkmessager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// Crypto preserves installed decrypt's pad-length slicing, including the absence
// of PKCS7 validation. The host must authenticate callbacks separately.
type Crypto struct {
	encryptKey string
	key        [32]byte
	configured bool
}

func NewCrypto(encryptKey string) *Crypto {
	return &Crypto{encryptKey: encryptKey, key: sha256.Sum256([]byte(encryptKey)), configured: encryptKey != ""}
}
func (c *Crypto) VerifySignature(signature string, body []byte, timestamp, nonce string) bool {
	if signature == "" || !c.configured || timestamp == "" || nonce == "" || !utf8.Valid(body) {
		return false
	}
	sum := sha256.Sum256(append([]byte(timestamp+nonce+c.encryptKey), body...))
	want := hex.EncodeToString(sum[:])
	signature = strings.ToLower(signature)
	for _, r := range signature {
		if r > 127 {
			return false
		}
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(signature)) == 1
}
func (c *Crypto) Decrypt(encoded string) (string, error) {
	if !c.configured {
		return "", errors.New("Lark encrypt key not configured")
	}
	raw, err := decodeBase64(encoded)
	if err != nil {
		return "", errors.New("Lark invalid ciphertext encoding")
	}
	if len(raw) < aes.BlockSize || (len(raw)-aes.BlockSize)%aes.BlockSize != 0 {
		return "", errors.New("Lark invalid ciphertext length")
	}
	out := make([]byte, len(raw)-aes.BlockSize)
	block, _ := aes.NewCipher(c.key[:])
	cipher.NewCBCDecrypter(block, raw[:aes.BlockSize]).CryptBlocks(out, raw[aes.BlockSize:])
	if len(out) == 0 {
		return "", errors.New("Lark empty ciphertext")
	}
	pad := int(out[len(out)-1])
	if pad == 0 || pad > len(out) {
		out = nil
	} else {
		out = out[:len(out)-pad]
	}
	if !utf8.Valid(out) {
		return "", errors.New("Lark invalid plaintext UTF8")
	}
	return string(out), nil
}
func (c *Crypto) Encrypt(plaintext string, random io.Reader) (string, error) {
	if !c.configured {
		return "", errors.New("Lark encrypt key not configured")
	}
	if !utf8.ValidString(plaintext) {
		return "", ErrUnsupported
	}
	if random == nil {
		random = rand.Reader
	}
	iv := make([]byte, aes.BlockSize)
	if _, e := io.ReadFull(random, iv); e != nil {
		return "", e
	}
	data := []byte(plaintext)
	pad := aes.BlockSize - len(data)%aes.BlockSize
	for i := 0; i < pad; i++ {
		data = append(data, byte(pad))
	}
	out := make([]byte, len(data))
	b, _ := aes.NewCipher(c.key[:])
	cipher.NewCBCEncrypter(b, iv).CryptBlocks(out, data)
	return base64.StdEncoding.EncodeToString(append(iv, out...)), nil
}

// CPython3.12.13 base64.b64decode -> binascii.a2b_base64(strict_mode=false).
// Ported from the already source-proven SMTP helper, itself derived from the
// fixed official CPython C algorithm; see reference/dependency licenses.
func decodeBase64(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ErrUnsupported
	}
	for _, r := range s {
		if r > 127 {
			return nil, ErrUnsupported
		}
	}
	out := make([]byte, 0, (len(s)+3)/4*3)
	position, left, pads := 0, byte(0), 0
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for _, c := range []byte(s) {
		if c == '=' {
			if position >= 2 {
				pads++
				if position+pads >= 4 {
					return out, nil
				}
			}
			continue
		}
		value := strings.IndexByte(alphabet, c)
		if value < 0 {
			continue
		}
		pads = 0
		v := byte(value)
		switch position {
		case 0:
			position = 1
			left = v
		case 1:
			position = 2
			out = append(out, (left<<2)|(v>>4))
			left = v & 15
		case 2:
			position = 3
			out = append(out, (left<<4)|(v>>2))
			left = v & 3
		case 3:
			position = 0
			out = append(out, (left<<6)|v)
			left = 0
		}
	}
	if position != 0 {
		return nil, ErrUnsupported
	}
	return out, nil
}
