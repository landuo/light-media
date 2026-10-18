package app

import "strings"

// Credentials remain bound to the same explicit daemon endpoint. A typed API
// key replaces the old value; an explicit clear is distinct from a blank
// public projection being saved by an unrelated settings section.
func preserveDownloaderCredentials(next *DownloaderConfig, old DownloaderConfig) {
	next.APIKey = strings.TrimSpace(next.APIKey)
	if next.ClearAPIKey {
		next.APIKey = ""
	} else if next.APIKey == "" && next.ID == old.ID && next.Kind == old.Kind && next.BaseURL == old.BaseURL {
		next.APIKey = old.APIKey
	}
	if next.Password == "" && next.ID == old.ID && next.Kind == old.Kind && next.BaseURL == old.BaseURL && next.Username == old.Username {
		next.Password = old.Password
	}
}
