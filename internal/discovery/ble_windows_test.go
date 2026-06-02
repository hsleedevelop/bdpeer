//go:build windows

package discovery

import "testing"

func TestWindowsBLEConnectCandidateIncludesManufacturerNicknameFallback(t *testing.T) {
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
			name: "manufacturer-only bdpeer advertisement",
			details: windowsScanDetails{
				manufacturerNick: "chad-dev",
				connectable:      false,
			},
			want: true,
		},
		{
			name: "service advertisement must be connectable without nickname fallback",
			details: windowsScanDetails{
				hasServiceUUID: true,
				connectable:    false,
			},
			want: false,
		},
		{
			name: "connectable unknown advertisement is not enough",
			details: windowsScanDetails{
				manufacturerNick: unknownBLENickname,
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

func TestWindowsBLEAdvertisementVisibilityIncludesManufacturerOnlyAdvertisements(t *testing.T) {
	if !isWindowsBLEVisibleAdvertisement(windowsScanDetails{
		manufacturerNick: "chad-dev",
		connectable:      false,
	}) {
		t.Fatal("manufacturer-only bdpeer advertisement should remain visible in scan logs")
	}
}
