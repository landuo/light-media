package app

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

// StartupProxyHost returns the saved proxy setting without constructing an App.
//
// The startup updater runs before the listener binds and before the instance
// lock is taken, so it cannot use the normal settings path. It reads the same
// encrypted record with the same key and associated data, and only extracts the
// proxy field: no other setting leaves this function.
func StartupProxyHost(dir string) (string, error) {
	key, err := os.ReadFile(filepath.Join(dir, "secret.key"))
	if err != nil {
		return "", err
	}
	if len(key) != 32 {
		return "", errors.New("invalid application encryption key")
	}
	database := filepath.Join(dir, "light-media.db")
	if _, statErr := os.Stat(database); statErr != nil {
		return "", statErr
	}
	readURL := url.URL{Scheme: "file", Path: database}
	readURL.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(ON)"}}.Encode()
	db, err := sql.Open("sqlite", readURL.String())
	if err != nil {
		return "", err
	}
	defer db.Close()
	var sealed []byte
	if err = db.QueryRow(`SELECT payload FROM systemconfig WHERE uid='main'`).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("configuration cannot be decrypted")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("light-media-config-v1"))
	if err != nil {
		return "", errors.New("configuration cannot be decrypted")
	}
	var settings struct {
		ProxyHost string `json:"proxy_host"`
	}
	if err = json.Unmarshal(plain, &settings); err != nil {
		return "", err
	}
	return settings.ProxyHost, nil
}
