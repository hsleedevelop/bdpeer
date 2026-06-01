package discovery

import (
	"testing"
	"time"
)

func TestConnectBackoffTableActiveUntilExpiry(t *testing.T) {
	table := newConnectBackoffTable()
	now := time.Unix(100, 0)
	until := table.Mark("AA:BB:CC:DD:EE:FF", now, 2*time.Minute)

	gotUntil, active := table.Active("AA:BB:CC:DD:EE:FF", now.Add(time.Minute))
	if !active {
		t.Fatal("expected backoff to be active")
	}
	if !gotUntil.Equal(until) {
		t.Fatalf("backoff until = %v, want %v", gotUntil, until)
	}

	if _, active := table.Active("AA:BB:CC:DD:EE:FF", now.Add(2*time.Minute)); active {
		t.Fatal("expected backoff to expire at deadline")
	}
}

func TestConnectBackoffTableClear(t *testing.T) {
	table := newConnectBackoffTable()
	now := time.Unix(100, 0)
	table.Mark("AA:BB:CC:DD:EE:FF", now, 2*time.Minute)
	table.Clear("AA:BB:CC:DD:EE:FF")

	if _, active := table.Active("AA:BB:CC:DD:EE:FF", now.Add(time.Minute)); active {
		t.Fatal("expected backoff to be cleared")
	}
}
