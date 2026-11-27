package plugins

import (
	"context"
	"errors"
)

const SitesMessagesMarkReadCapability = "sites.messages.mark_read"

type SiteMessagesMarkReadRequestV1 struct {
	SiteID string   `json:"site_id"`
	Titles []string `json:"titles"`
}

type SiteMessagesMarkReadResultV1 struct {
	SchemaVersion int    `json:"schema_version"`
	Scanned       int    `json:"scanned"`
	Matched       int    `json:"matched"`
	Marked        int    `json:"marked"`
	Failed        int    `json:"failed"`
	SkippedReason string `json:"skipped_reason,omitempty"`
	ErrorReason   string `json:"error_reason,omitempty"`
}

func (s Sites) MarkMessagesRead(ctx context.Context, request SiteMessagesMarkReadRequestV1) (SiteMessagesMarkReadResultV1, error) {
	var out SiteMessagesMarkReadResultV1
	err := hostBusinessCall(ctx, s.Host, SitesMessagesMarkReadCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Scanned < 0 || out.Matched < 0 || out.Marked < 0 || out.Failed < 0 ||
		out.Matched > out.Scanned || out.Marked > out.Matched || out.Failed > out.Matched-out.Marked) {
		err = errors.New("invalid site message read response")
	}
	return out, err
}
