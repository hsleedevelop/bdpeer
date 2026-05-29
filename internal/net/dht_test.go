package net

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestDHTStatusLogsAreRateLimited(t *testing.T) {
	h := &Host{}
	now := time.Unix(1000, 0)

	if !h.shouldLogDHTStatus(&h.dhtSearchLog, now) {
		t.Fatal("first DHT status log was suppressed")
	}
	if h.shouldLogDHTStatus(&h.dhtSearchLog, now.Add(dhtStatusLogInterval-time.Second)) {
		t.Fatal("DHT status log was not rate limited")
	}
	if !h.shouldLogDHTStatus(&h.dhtSearchLog, now.Add(dhtStatusLogInterval)) {
		t.Fatal("DHT status log did not resume after interval")
	}
}

func TestDHTPeerRetryBackoffSuppressesRecentFailures(t *testing.T) {
	h := &Host{}
	id := peer.ID("peer-a")
	now := time.Unix(1000, 0)

	if !h.shouldRetryDHTPeer(id, now) {
		t.Fatal("first DHT peer attempt was suppressed")
	}

	h.recordDHTConnectFailure(id, now)
	if h.shouldRetryDHTPeer(id, now.Add(dhtConnectFailureBackoff-time.Second)) {
		t.Fatal("DHT peer retry was not backed off after failure")
	}
	if !h.shouldRetryDHTPeer(id, now.Add(dhtConnectFailureBackoff)) {
		t.Fatal("DHT peer retry did not resume after backoff")
	}

	h.recordDHTConnectSuccess(id)
	if !h.shouldRetryDHTPeer(id, now.Add(time.Second)) {
		t.Fatal("DHT peer retry remained suppressed after success")
	}
}
