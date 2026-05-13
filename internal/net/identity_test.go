package net_test

import (
	"testing"

	bnet "github.com/chad/bdpeer/internal/net"
)

func TestPrivateKeyRoundTrip(t *testing.T) {
	encoded, err := bnet.GenerateIdentityB64()
	if err != nil {
		t.Fatal(err)
	}
	first, err := bnet.PrivateKeyFromB64(encoded)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bnet.PrivateKeyFromB64(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !first.GetPublic().Equals(second.GetPublic()) {
		t.Fatal("expected persisted private key to produce stable public identity")
	}
}
