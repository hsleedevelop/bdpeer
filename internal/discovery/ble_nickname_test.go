package discovery

import "testing"

func TestNormalizeBLENickname(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "plain", data: []byte("sean-dev"), want: "sean-dev"},
		{name: "trim", data: []byte("  chad-work  "), want: "chad-work"},
		{name: "empty", data: []byte("  "), want: unknownBLENickname},
		{name: "binary control", data: []byte{0x16, 0x01, 0x1e, 0x00, 'N', 'F', 'F', 'P'}, want: unknownBLENickname},
		{name: "invalid utf8", data: []byte{0xff, 0xfe}, want: unknownBLENickname},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeBLENickname(tt.data); got != tt.want {
				t.Fatalf("normalizeBLENickname() = %q, want %q", got, tt.want)
			}
		})
	}
}
