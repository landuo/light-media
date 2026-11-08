package sites

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"light-media/internal/metainfo"
)

var subscriptionDownloadForm = regexp.MustCompile(`(?s)<form\b[^>]*?\saction="([^"]*)"[^>]*>(.*?)</form>`)
var subscriptionDownloadInput = regexp.MustCompile(`(?s)<input\b[^>]*?\sname="([^"]*)"[^>]*?\svalue="([^"]*)"[^>]*>`)

// The fixed first-download prompt accepts only the first form with action
// "?" and its explicit, double-quoted name/value pairs. Submit them once to
// the exact URL that returned the prompt, retaining only its existing cookie.
func (c *Client) confirmSubscriptionTorrent(ctx context.Context, u *url.URL, headers map[string]string, data []byte) ([]byte, error) {
	if !bytes.Contains(data, []byte("下载种子文件")) {
		return data, nil
	}
	if u == nil || u.Opaque != "" || !sameOrigin(u, c.base) || len(u.String()) > 8192 {
		return nil, errors.New("site_material_origin_mismatch")
	}
	failure := errors.New("site_first_download_confirmation_failed")
	if len(data) > htmlLimit {
		return nil, failure
	}
	var fields url.Values
	for _, form := range subscriptionDownloadForm.FindAllSubmatch(data, -1) {
		if !bytes.Equal(form[1], []byte("?")) {
			continue
		}
		if len(form[2]) > 64<<10 {
			return nil, failure
		}
		fields = url.Values{}
		for _, input := range subscriptionDownloadInput.FindAllSubmatch(form[2], -1) {
			fields.Set(string(input[1]), string(input[2]))
		}
		break
	}
	if len(fields) == 0 {
		return nil, markSubscriptionMaterialFailure(failure)
	}
	postHeaders := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if cookie := headers["Cookie"]; cookie != "" {
		postHeaders["Cookie"] = cookie
	}
	var reply BrushRecipeResponse
	body, _, err := c.requestWithBrushReply(ctx, http.MethodPost, u, []byte(fields.Encode()), postHeaders, torrentLimit, &reply)
	if subscriptionMaterialHTTPFailure(reply.Status) {
		if err != nil {
			failure = fmt.Errorf("%w: %w", failure, err)
		}
		return nil, markSubscriptionMaterialFailure(failure)
	}
	if err != nil {
		return nil, err
	}
	if reply.Status != http.StatusOK {
		return nil, failure
	}
	if _, err := metainfo.ProductInspect(body); err != nil {
		return nil, markSubscriptionMaterialFailure(failure)
	}
	return body, nil
}
