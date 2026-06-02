//go:build windows

package discovery

import "testing"

func TestWindowsBLEConnectCandidateRequiresConnectableServiceAdvertisement(t *testing.T) {
	tests := []struct {
		name    string
		details windowsScanDetails
		want    bool
	}{
		{
			name: "connectable service advertisement",
			details: windowsScanDetails{
				hasServiceUUID: true,
				connectable:    true,
			},
			want: true,
		},
		{
			name: "manufacturer-only advertisement is visible but not connectable",
			details: windowsScanDetails{
				manufacturerNick: "chad-dev",
				connectable:      false,
			},
			want: false,
		},
		{
			name: "service advertisement must be connectable",
			details: windowsScanDetails{
				hasServiceUUID: true,
				connectable:    false,
			},
			want: false,
		},
		{
			name: "connectable foreign advertisement is not enough",
			details: windowsScanDetails{
				manufacturerNick: "chad-dev",
				connectable:      true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsBLEConnectCandidate(tt.details); got != tt.want {
				t.Fatalf("isWindowsBLEConnectCandidate() = %v, want %v", got, tt.want)
			}
		})
	}
}
