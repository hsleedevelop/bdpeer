package transport

import (
	"context"
	"io"
	"testing"
)

type fakeTransport struct{ name string }

func (f *fakeTransport) OpenStream(_ context.Context, _ PeerID) (io.ReadWriteCloser, error) {
	return nil, nil
}
func (f *fakeTransport) SetHandler(_ Handler) {}
func (f *fakeTransport) Close(_ PeerID) error { return nil }
func (f *fakeTransport) Name() string         { return f.name }

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	tA := &fakeTransport{name: "A"}
	r.Register("peer1", tA)

	got, ok := r.Lookup("peer1")
	if !ok {
		t.Fatalf("expected peer1 to be registered")
	}
	if got.Name() != "A" {
		t.Fatalf("expected transport A, got %s", got.Name())
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register("peer1", &fakeTransport{name: "A"})
	r.Unregister("peer1")
	if _, ok := r.Lookup("peer1"); ok {
		t.Fatalf("expected peer1 to be unregistered")
	}
}

func TestRegistry_RegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register("peer1", &fakeTransport{name: "A"})
	r.Register("peer1", &fakeTransport{name: "B"})
	got, _ := r.Lookup("peer1")
	if got.Name() != "B" {
		t.Fatalf("expected overwrite to B, got %s", got.Name())
	}
}

func TestRegistry_LookupMissing(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup("nope"); ok {
		t.Fatalf("expected miss")
	}
}
