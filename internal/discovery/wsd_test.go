package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
)

func TestWSDHello(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := discovery.SendWSDHello(ctx, "alice", 4001)
	if err != nil {
		t.Fatalf("SendWSDHello: %v", err)
	}
}
