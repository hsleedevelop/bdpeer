package core

import (
	"testing"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/config"
)

func newAckTestService() *Service {
	return NewService(&config.Config{Nickname: "alice"}, "")
}

// TestAck_DeliverSuccess verifies that a normal ACK with an empty error string
// resolves the registered channel with a nil error — the success path that
// EventFileDone depends on.
func TestAck_DeliverSuccess(t *testing.T) {
	s := newAckTestService()
	ch := s.registerAck("xfer-1")
	s.deliverAck("xfer-1", "")

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("expected nil err on success ACK, got %v", res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK channel did not receive within timeout")
	}

	// Bookkeeping: the entry must be removed so a duplicate ACK is a no-op.
	s.acksMu.Lock()
	_, present := s.acks["xfer-1"]
	s.acksMu.Unlock()
	if present {
		t.Fatal("ack entry not cleaned up after deliver")
	}
}

// TestAck_DeliverReceiverError verifies that a non-empty error string from the
// receiver propagates through the channel — covers the FILE_ACK error path
// (e.g. disk full, write failure on receiver side).
func TestAck_DeliverReceiverError(t *testing.T) {
	s := newAckTestService()
	ch := s.registerAck("xfer-2")
	s.deliverAck("xfer-2", "disk full")

	select {
	case res := <-ch:
		if res.err == nil {
			t.Fatal("expected error from receiver ACK, got nil")
		}
		if res.err.Error() != "disk full" {
			t.Fatalf("expected error %q, got %q", "disk full", res.err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("ACK channel did not receive within timeout")
	}
}

// TestAck_DropPreventsDeliver verifies that dropAck (called on outbound
// failure or context cancellation) removes the entry so a late ACK arriving
// over the wire is silently discarded instead of blocking on a closed channel.
func TestAck_DropPreventsDeliver(t *testing.T) {
	s := newAckTestService()
	ch := s.registerAck("xfer-3")
	s.dropAck("xfer-3")

	// Late ACK after drop must be a no-op.
	s.deliverAck("xfer-3", "")

	select {
	case res := <-ch:
		t.Fatalf("ACK channel should not have received after drop, got %v", res)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

// TestAck_DeliverUnknownIDIsNoop verifies that a FILE_ACK arriving for an
// unknown transfer id (sender already gave up, or duplicate ACK after success)
// does not panic.
func TestAck_DeliverUnknownIDIsNoop(t *testing.T) {
	s := newAckTestService()
	s.deliverAck("nonexistent", "")
	s.deliverAck("nonexistent", "err")
}

// TestAck_TimeoutPathDoesNotLeak verifies the sender-side timeout cleanup —
// after a Send times out and calls dropAck, a subsequently arriving ACK must
// not deliver into the (now-abandoned) channel.
func TestAck_TimeoutPathDoesNotLeak(t *testing.T) {
	s := newAckTestService()
	ch := s.registerAck("xfer-4")

	// Simulate the timeout branch in Send: dropAck and discard ch.
	s.dropAck("xfer-4")

	// Late ACK after timeout cleanup.
	s.deliverAck("xfer-4", "")

	select {
	case res := <-ch:
		t.Fatalf("channel must not receive after timeout drop, got %v", res)
	case <-time.After(50 * time.Millisecond):
	}

	s.acksMu.Lock()
	if len(s.acks) != 0 {
		t.Fatalf("acks map not empty after drop: %d entries", len(s.acks))
	}
	s.acksMu.Unlock()
}
