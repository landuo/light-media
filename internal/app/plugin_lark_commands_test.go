package app

import (
	"encoding/json"
	"errors"
	"light-media/internal/notice"
	"testing"
)

func TestPluginNoticeAbsentContextCannotPretendTMDBProjection(t *testing.T) {
	good := sourceBusinessNotice{AbsentContext: true, Envelope: notice.Object{"title": "owned"}, Kwargs: notice.Object{}}
	input, e := larkBusinessNoticeContext(good)
	if e != nil || input.Meta != nil || input.Media != nil || input.Torrent != nil || input.Transfer != nil {
		t.Fatal(input, e)
	}
	for _, field := range []string{"meta", "media", "provider", "registration", "catalog"} {
		t.Run(field, func(t *testing.T) {
			bad := good
			switch field {
			case "meta":
				bad.Meta = json.RawMessage(`{}`)
			case "media":
				bad.MediaInfo = json.RawMessage(`{}`)
			case "provider":
				bad.ProviderInfo = json.RawMessage(`{}`)
			case "registration":
				bad.Registration = json.RawMessage(`{}`)
			case "catalog":
				bad.Media.ID = 1
			}
			if _, e := larkBusinessNoticeContext(bad); !errors.Is(e, notice.ErrTemplateDependency) {
				t.Fatal("partial object accepted as original None", field, e)
			}
		})
	}
}
