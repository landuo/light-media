package mediaservervendors

import (
	"encoding/json"
	"light-media/internal/plugins"
	"strings"
)

// Retained for source compatibility only. Ordinary constructor JSON no longer
// passes through this unfinished admission restriction.
type PlexArgumentBoundary struct{ Field string }

func (e *PlexArgumentBoundary) Error() string {
	return "Plex argument domain not yet implemented: " + e.Field
}

type plexConstructorArguments struct {
	host, token, playHost, syncLibraries json.RawMessage
	incomplete                           bool
}

func plexArguments(config plugins.MediaServerConfig) (plexConstructorArguments, error) {
	var out plexConstructorArguments
	fields, err := constructorFields(config.Config)
	if err != nil {
		return out, err
	}
	if !ejTruth(fields["host"]) || !ejTruth(fields["token"]) {
		out.incomplete = true
		return out, nil
	}
	out.host = constructorJSON(fields["host"])
	out.token = constructorJSON(fields["token"])
	out.playHost = constructorJSON(fields["play_host"])
	out.syncLibraries, err = constructorLibraries(config.SyncLibraries)
	return out, err
}
func plexStandardizeBaseURL(host string) string {
	if host == "" {
		return host
	}
	if !strings.HasSuffix(host, "/") {
		host += "/"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return host
}
