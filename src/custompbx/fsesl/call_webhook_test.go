package fsesl

import (
	"testing"

	"custompbx/mainStruct"
)

func TestCallBusinessMetadataOutboundFromExtension(t *testing.T) {
	extension, direction, customer := callBusinessMetadata(nil, nil, "1002", "0342387314")
	if extension != "1002" || direction != "outbound" || customer != "0342387314" {
		t.Fatalf("got extension=%q direction=%q customer=%q", extension, direction, customer)
	}
}

func TestCallBusinessMetadataInboundToExtension(t *testing.T) {
	extension, direction, customer := callBusinessMetadata(nil, nil, "0342387314", "1002")
	if extension != "1002" || direction != "inbound" || customer != "0342387314" {
		t.Fatalf("got extension=%q direction=%q customer=%q", extension, direction, customer)
	}
}

func TestCallBusinessMetadataUsesPresenceID(t *testing.T) {
	channel := &mainStruct.Channel{PresenceId: "1002@td1.tekomi.vn"}
	extension, direction, customer := callBusinessMetadata(nil, channel, "1002", "84901234567")
	if extension != "1002" || direction != "outbound" || customer != "84901234567" {
		t.Fatalf("got extension=%q direction=%q customer=%q", extension, direction, customer)
	}
}

func TestFirstPresentReturnsCanonicalLinkedID(t *testing.T) {
	if got := firstPresent("", "linked-call", "leg-call"); got != "linked-call" {
		t.Fatalf("firstPresent = %q", got)
	}
}
