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

func TestPrimaryCallLegSelection(t *testing.T) {
	tests := []struct {
		name  string
		event map[string]string
		want  bool
	}{
		{
			name:  "inbound originating leg",
			event: map[string]string{NameChannelDirection: "inbound"},
			want:  true,
		},
		{
			name: "forked device leg",
			event: map[string]string{
				NameChannelDirection:          "outbound",
				NameChannelOriginatingLegUUID: "a-leg-uuid",
			},
			want: false,
		},
		{
			name: "bridged outbound leg",
			event: map[string]string{
				NameChannelDirection:    "outbound",
				NameChannelOtherLegUuid: "a-leg-uuid",
			},
			want: false,
		},
		{
			name:  "standalone originate fallback",
			event: map[string]string{NameChannelDirection: "outbound"},
			want:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isPrimaryCallLeg(test.event); got != test.want {
				t.Fatalf("isPrimaryCallLeg() = %v, want %v", got, test.want)
			}
		})
	}
}
