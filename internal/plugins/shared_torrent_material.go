package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const DownloadTorrentCapability = "chain.download.download_torrent"
const TorrentMaterialChunkBytes = 1 << 20
const TorrentMaterialMaxBytes = 16 << 20

// DownloadTorrentRequest retains the private source TorrentInfo object. It is
// not a grant to change site credentials, trusted origins or proxy routing.
// UserID is the source's string/integer/null argument, not a float conversion.
type DownloadTorrentRequest struct {
	Torrent json.RawMessage `json:"torrent"`
	Channel *string         `json:"channel"`
	Source  *string         `json:"source"`
	UserID  json.RawMessage `json:"userid"`
}

// A nil Content is source None. Kind="bytes" with an empty non-nil Bytes is
// different from None; Kind="string" preserves the exact returned string.
type TorrentMaterialContent struct {
	Kind   string
	Bytes  []byte
	String string
}
type TorrentMaterialResult struct {
	Content *TorrentMaterialContent
	Folder  string
	Files   []string
}

// DownloadTorrentSourceError reports only a classified source exception. No
// descriptor, credential, response body or Python traceback is transported.
type DownloadTorrentSourceError struct{ Class string }

func (e *DownloadTorrentSourceError) Error() string { return "download torrent source: " + e.Class }
func validDownloadTorrentSourceError(class string) bool {
	switch class {
	case "Error", "JSONDecodeError", "UnicodeDecodeError", "UnicodeEncodeError", "AttributeError", "TypeError", "ValueError", "KeyError":
		return true
	}
	return false
}

type SharedDownloadChain struct{ Host HostCaller }

var torrentMaterialHandlePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// DownloadTorrent is one public logical operation. Method-private reads carry
// at most 1MiB raw bytes each, preserving the existing 16MiB material allowance
// without raising the SDK's JSON/frame/wire budgets. Source Python has no equal
// material-size cap: this bounded adapter does not claim arbitrary-size parity.
func (c SharedDownloadChain) DownloadTorrent(ctx context.Context, input DownloadTorrentRequest) (out TorrentMaterialResult, err error) {
	if ctx == nil || c.Host == nil {
		return out, errors.New("download torrent requires an active context and host caller")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !validHistoryQueryStrings(input.Channel, input.Source) || !utf8.Valid(input.Torrent) || !utf8.Valid(input.UserID) {
		return out, errors.New("invalid download torrent text")
	}
	var torrent map[string]json.RawMessage
	if json.Unmarshal(input.Torrent, &torrent) != nil || torrent == nil {
		return out, errors.New("download torrent requires a TorrentInfo object")
	}
	if len(input.UserID) == 0 {
		input.UserID = json.RawMessage(`null`)
	}
	var user any
	d := json.NewDecoder(bytes.NewReader(input.UserID))
	d.UseNumber()
	if d.Decode(&user) != nil || d.Decode(new(any)) != io.EOF {
		return out, errors.New("invalid download torrent userid")
	}
	switch value := user.(type) {
	case nil, string:
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			return out, errors.New("download torrent userid must be an integer or string")
		}
	default:
		return out, errors.New("download torrent userid must be an integer or string")
	}
	request, err := json.Marshal(struct {
		Operation string                 `json:"op"`
		Input     DownloadTorrentRequest `json:"input"`
	}{"prepare", input})
	if err != nil {
		return out, err
	}
	if len(request) > 128<<10 {
		return out, errors.New("download torrent input exceeds host limit")
	}
	raw, err := c.Host(ctx, DownloadTorrentCapability, request)
	if err != nil {
		return out, err
	}
	// A malformed result may still identify this call's allocated transfer.
	// Release only a syntactically valid opaque token; host authority is still
	// current-invocation bound. An expired scope cannot be revived by cleanup.
	var cleanup struct {
		Handle string `json:"handle"`
	}
	if len(raw) <= MaxPayloadBytes && utf8.Valid(raw) && json.Unmarshal(raw, &cleanup) == nil && torrentMaterialHandlePattern.MatchString(cleanup.Handle) {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			payload, _ := json.Marshal(struct {
				Operation string `json:"op"`
				Handle    string `json:"handle"`
			}{"release", cleanup.Handle})
			released, releaseErr := c.Host(cleanupCtx, DownloadTorrentCapability, payload)
			if releaseErr == nil {
				var receipt struct {
					Released *bool `json:"released"`
				}
				releaseErr = torrentMaterialDecode(released, &receipt)
				if releaseErr == nil && (receipt.Released == nil || !*receipt.Released) {
					releaseErr = errors.New("download torrent release was not acknowledged")
				}
			}
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil {
				err = releaseErr
			}
			if err != nil {
				out = TorrentMaterialResult{}
			}
		}()
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	var prepared struct {
		Kind        *string    `json:"kind"`
		SourceError *string    `json:"source_error,omitempty"`
		Folder      *string    `json:"folder"`
		Files       *[]*string `json:"files"`
		Handle      *string    `json:"handle,omitempty"`
		Total       *int       `json:"total,omitempty"`
		SHA256      *string    `json:"sha256,omitempty"`
		Text        *string    `json:"text,omitempty"`
	}
	if err = torrentMaterialDecode(raw, &prepared); err != nil {
		return out, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if prepared.Kind != nil && *prepared.Kind == "source_error" {
		if prepared.SourceError == nil || !validDownloadTorrentSourceError(*prepared.SourceError) || len(fields) != 2 {
			return out, errors.New("invalid download torrent source exception")
		}
		return out, &DownloadTorrentSourceError{Class: *prepared.SourceError}
	}
	if _, present := fields["source_error"]; present {
		return out, errors.New("mixed download torrent source exception")
	}
	if prepared.Kind == nil || prepared.Folder == nil || prepared.Files == nil || *prepared.Files == nil {
		return out, errors.New("incomplete download torrent result")
	}
	for _, key := range []string{"handle", "total", "sha256", "text"} {
		_, present := fields[key]
		allowed := *prepared.Kind == "bytes" && key != "text" || *prepared.Kind == "string" && key == "text"
		if present && !allowed {
			return out, errors.New("mixed download torrent result kinds")
		}
	}
	out.Folder, out.Files = *prepared.Folder, make([]string, 0, len(*prepared.Files))
	for _, file := range *prepared.Files {
		if file == nil {
			return TorrentMaterialResult{}, errors.New("download torrent filename cannot be null")
		}
		out.Files = append(out.Files, *file)
	}
	switch *prepared.Kind {
	case "none":
		return out, nil
	case "string":
		if prepared.Text == nil {
			return TorrentMaterialResult{}, errors.New("download torrent string is absent")
		}
		out.Content = &TorrentMaterialContent{Kind: "string", String: *prepared.Text}
		return out, nil
	case "bytes":
		if prepared.Handle == nil || !torrentMaterialHandlePattern.MatchString(*prepared.Handle) || prepared.Total == nil || *prepared.Total < 0 || *prepared.Total > TorrentMaterialMaxBytes || prepared.SHA256 == nil {
			return TorrentMaterialResult{}, errors.New("invalid download torrent byte transfer")
		}
		digest, decodeErr := hex.DecodeString(*prepared.SHA256)
		if decodeErr != nil || len(digest) != sha256.Size || strings.ToLower(*prepared.SHA256) != *prepared.SHA256 {
			return TorrentMaterialResult{}, errors.New("invalid download torrent byte digest")
		}
		content := make([]byte, 0, *prepared.Total)
		for len(content) < *prepared.Total {
			if err = ctx.Err(); err != nil {
				return TorrentMaterialResult{}, err
			}
			read, _ := json.Marshal(struct {
				Operation string `json:"op"`
				Handle    string `json:"handle"`
				Offset    int    `json:"offset"`
			}{"read", *prepared.Handle, len(content)})
			raw, err = c.Host(ctx, DownloadTorrentCapability, read)
			if err != nil {
				return TorrentMaterialResult{}, err
			}
			if err = ctx.Err(); err != nil {
				return TorrentMaterialResult{}, err
			}
			var chunk struct {
				Handle *string         `json:"handle"`
				Offset *int            `json:"offset"`
				Data   json.RawMessage `json:"data"`
			}
			if err = torrentMaterialDecode(raw, &chunk); err != nil {
				return TorrentMaterialResult{}, err
			}
			if chunk.Handle == nil || *chunk.Handle != *prepared.Handle || chunk.Offset == nil || *chunk.Offset != len(content) || len(chunk.Data) == 0 || chunk.Data[0] != '"' {
				return TorrentMaterialResult{}, errors.New("download torrent chunk identity mismatch")
			}
			var data []byte
			if err = json.Unmarshal(chunk.Data, &data); err != nil {
				return TorrentMaterialResult{}, err
			}
			if len(data) != min(TorrentMaterialChunkBytes, *prepared.Total-len(content)) {
				return TorrentMaterialResult{}, errors.New("download torrent chunk length mismatch")
			}
			content = append(content, data...)
		}
		actual := sha256.Sum256(content)
		if !bytes.Equal(actual[:], digest) {
			return TorrentMaterialResult{}, errors.New("download torrent byte digest mismatch")
		}
		out.Content = &TorrentMaterialContent{Kind: "bytes", Bytes: content}
		return out, nil
	default:
		return TorrentMaterialResult{}, errors.New("unknown download torrent result kind")
	}
}

func torrentMaterialDecode(raw json.RawMessage, target any) error {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes || !utf8.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("invalid download torrent wire response")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing download torrent response")
	}
	return nil
}
