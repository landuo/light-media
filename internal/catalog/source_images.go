package catalog

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// SourceImages uses the existing fixed-origin authenticated metadata client.
// The pinned image helper's language list is distinct from detail localization.
func (c *Client) SourceImages(ctx context.Context, kind string, id int64, locale, originalLanguage string) (json.RawMessage, error) {
	if !kindOK(kind) || !validID(id) {
		return nil, &Error{Code: "invalid_input"}
	}
	languages := []string{}
	seen := map[string]bool{}
	for _, language := range []string{locale, "en", "null", originalLanguage} {
		language = strings.TrimSpace(language)
		if language != "" && !seen[language] {
			languages = append(languages, language)
			seen[language] = true
		}
	}
	var raw json.RawMessage
	err := c.get(ctx, kind+"/"+strconv.FormatInt(id, 10)+"/images", url.Values{"include_image_language": {strings.Join(languages, ",")}}, &raw)
	return raw, err
}
