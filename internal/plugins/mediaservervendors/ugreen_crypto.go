package mediaservervendors

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
)

// Pure private cryptography. Production assembly must explicitly pass its
// cryptographic random source for UUID/IV. Go1.26 RSA uses secure stdlib randomness
// regardless of this reader by default; no GODEBUG/global random changes.
// Keys are synthetic/owned in tests; no filesystem, HTTP, global CA or session.
type ugreenCipher struct {
	publicKey any
	entropy   io.Reader
}

func ugreenNormalizePublicKey(key string) (string, error) {
	if !utf8.ValidString(key) {
		return "", ejSource("UnicodeEncodeError")
	}
	key = strings.ReplaceAll(strings.Trim(legacyanime.Strip(key), `"`), `\n`, "\n")
	if strings.Contains(key, "BEGIN") {
		if !strings.HasSuffix(key, "\n") {
			key += "\n"
		}
		return key, nil
	}
	return "-----BEGIN RSA PUBLIC KEY-----\n" + key + "\n-----END RSA PUBLIC KEY-----\n", nil
}
func newUgreenCipher(publicKey string, entropy io.Reader) (*ugreenCipher, error) {
	if entropy == nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen requires real cryptographic entropy"}
	}
	text, err := ugreenNormalizePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, ejSource("ValueError")
	}
	var key any
	switch block.Type {
	case "RSA PUBLIC KEY":
		key, err = x509.ParsePKCS1PublicKey(block.Bytes)
	case "PUBLIC KEY":
		key, err = x509.ParsePKIXPublicKey(block.Bytes)
	default:
		return nil, ejSource("ValueError")
	}
	if err != nil {
		return nil, ejSource("ValueError")
	}
	return &ugreenCipher{publicKey: key, entropy: entropy}, nil
}
func (c *ugreenCipher) rsaEncryptLong(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if !utf8.ValidString(plaintext) {
		return "", ejSource("UnicodeEncodeError")
	}
	key, ok := c.publicKey.(*rsa.PublicKey)
	if !ok {
		return "", ejSource("AttributeError")
	}
	// The original uses key_size // 8, not ceil or a made-up chunk quota.
	width := key.N.BitLen() / 8
	chunk := width - 11
	if chunk <= 0 {
		return "", ejSource("ValueError")
	}
	raw := []byte(plaintext)
	encrypted := []byte{}
	for start := 0; start < len(raw); start += chunk {
		end := start + chunk
		if end > len(raw) {
			end = len(raw)
		}
		part, err := rsa.EncryptPKCS1v15(c.entropy, key, raw[start:end])
		if err != nil {
			return "", err
		}
		encrypted = append(encrypted, part...)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}
func (c *ugreenCipher) generateAESKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(c.entropy, raw); err != nil {
		return "", err
	}
	// uuid.uuid4().hex: random UUID's version/variant bits, without hyphens.
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw), nil
}
func ugreenAES(key string) (cipher.AEAD, error) {
	if !utf8.ValidString(key) {
		return nil, ejSource("UnicodeEncodeError")
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, ejSource("ValueError")
	}
	return cipher.NewGCM(block)
}
func (c *ugreenCipher) encryptAES(plaintext, key string) (string, error) {
	if !utf8.ValidString(plaintext) {
		return "", ejSource("UnicodeEncodeError")
	}
	// Source obtains the IV before constructing AESGCM; retain this ordering.
	iv := make([]byte, 12)
	if _, err := io.ReadFull(c.entropy, iv); err != nil {
		return "", err
	}
	aead, err := ugreenAES(key)
	if err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, iv, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(append(iv, sealed...)), nil
}
func ugreenDecryptAES(payload, key string) (string, error) {
	raw, err := ugreenBase64Decode(payload)
	if err != nil {
		return "", err
	}
	width := 12
	if len(raw) < width {
		width = len(raw)
	}
	iv, encrypted := raw[:width], raw[width:]
	aead, err := ugreenAES(key)
	if err != nil {
		return "", err
	}
	// cryptography allows 8..128 byte nonces, although successful source wire
	// always carries twelve. Short payloads have no tag and fail authentication.
	if len(iv) < 8 {
		return "", ejSource("ValueError")
	}
	if len(iv) != aead.NonceSize() {
		return "", ejSource("InvalidTag")
	}
	plain, err := aead.Open(nil, iv, encrypted, nil)
	if err != nil {
		return "", ejSource("InvalidTag")
	}
	if !utf8.Valid(plain) {
		return "", ejSource("UnicodeDecodeError")
	}
	return string(plain), nil
}
func ugreenSecurityKey(token string) (string, error) {
	if !utf8.ValidString(token) {
		return "", ejSource("UnicodeEncodeError")
	}
	sum := md5.Sum([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}
func (c *ugreenCipher) encryptBodyJSON(raw []byte, key string) ([]byte, error) {
	plain, err := ugreenNormalizeBody(raw)
	if err != nil {
		return nil, err
	}
	encrypted, err := c.encryptAES(plain, key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(plain))
	return []byte(`{"encrypt_req_body":` + string(ugreenKey(encrypted)) + `,"req_body_sha256":` + string(ugreenKey(hex.EncodeToString(sum[:]))) + `}`), nil
}
