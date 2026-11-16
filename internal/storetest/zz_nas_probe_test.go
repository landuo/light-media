package storetest

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestZZNasConfigProbe(t *testing.T) {
	key, err := os.ReadFile("/tmp/nas-lm/secret.key")
	if err != nil || len(key) != 32 {
		t.Fatalf("key: %v", err)
	}
	db, err := sql.Open("sqlite", "file:/tmp/nas-lm/light-media.db?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var payload []byte
	if err := db.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&payload); err != nil {
		t.Fatalf("config row: %v", err)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], []byte("light-media-config-v1"))
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	var cfg struct {
		TMDB struct {
			APIKey      string `json:"api_key"`
			AccessToken string `json:"access_token"`
			Domain      string `json:"domain"`
		} `json:"tmdb"`
		ProxyHost string `json:"proxy_host"`
		Sites     []struct {
			Name string `json:"name"`
		} `json:"sites"`
		Downloaders []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"downloaders"`
		AppDomain string `json:"app_domain"`
	}
	if err := json.Unmarshal(plain, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	t.Logf("tmdb key len=%d token len=%d domain=%q proxy=%v sites=%d downloaders=%d app_domain=%v",
		len(cfg.TMDB.APIKey), len(cfg.TMDB.AccessToken), cfg.TMDB.Domain, cfg.ProxyHost != "", len(cfg.Sites), len(cfg.Downloaders), cfg.AppDomain != "")
	// Hand the values to the local test instance without printing them.
	var b strings.Builder
	b.WriteString("TMDB_API_KEY=" + cfg.TMDB.APIKey + "\n")
	b.WriteString("TMDB_ACCESS_TOKEN=" + cfg.TMDB.AccessToken + "\n")
	b.WriteString("TMDB_DOMAIN=" + cfg.TMDB.Domain + "\n")
	b.WriteString("PROXY_HOST=" + cfg.ProxyHost + "\n")
	if err := os.WriteFile("/tmp/tmdb.env", []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
