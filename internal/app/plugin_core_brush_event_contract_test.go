package app

import (
	"strings"
	"testing"
)

func TestCoreDownloadDeletedRejectsBrushProducerReferenceAfterComposition(t *testing.T) {
	id := "dde_" + strings.Repeat("a", 64)
	event := DomainEvent{Type: "download.deleted", DownloadDeletedID: id, EventID: domainEventID("download.deleted", id)}
	if _, err := validateDomainEvent(event); err != nil {
		t.Fatal("valid core producer fixture rejected", err)
	}
	event.PluginTriggerID = strings.Repeat("b", 64)
	if _, err := validateDomainEvent(event); err == nil {
		t.Fatal("core event accepted another producer's Brush reference")
	}
}
