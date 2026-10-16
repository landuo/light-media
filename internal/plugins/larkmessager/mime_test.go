package larkmessager

import (
	"context"
	"encoding/base64"
)

// Controlled Host boundary for the original _upload_remote_image reference.
// Upload MIME/extension/data are checked, never a random temporary basename.
type imageMIMEHost struct {
	header          string
	calls, uploaded []any
}

func (h *imageMIMEHost) API(_ context.Context, r Request) (Response, error) {
	if r.Op != "upload_image" || r.Blob == nil {
		return Response{}, ErrDenied
	}
	b := r.Blob
	h.uploaded = append(h.uploaded, Object{"suffix": SourceSuffix(b.Name), "mime": b.MIME, "base64": base64.StdEncoding.EncodeToString(b.Data)})
	return Response{Status: 200, Body: []byte(`{"code":0,"data":{"image_key":"img_owned"}}`)}, nil
}
func (*imageMIMEHost) ReadFile(context.Context, string) (Blob, error) { return Blob{}, ErrDenied }
func (h *imageMIMEHost) FetchImage(_ context.Context, ref string) (Blob, error) {
	h.calls = append(h.calls, Object{"url": ref, "timeout": 30})
	return Blob{Name: "trusted-response", MIME: h.header, Data: []byte("owned-image")}, nil
}
