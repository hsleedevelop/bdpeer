package transport

import (
	"context"
	"fmt"
	"io"
	"sync"

	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Libp2pTransport wraps a *bnet.Host so that the libp2p stream protocol
// /bdpeer/1.0.0 can be used through the Transport interface.
//
// PeerID values used with this transport must be valid libp2p peer.ID
// strings (i.e. peer.ID.String() output).
type Libp2pTransport struct {
	host     *bnet.Host
	handOnce sync.Once
}

// NewLibp2pTransport returns a transport that uses host's libp2p endpoint.
func NewLibp2pTransport(host *bnet.Host) *Libp2pTransport {
	return &Libp2pTransport{host: host}
}

// Name implements Transport.
func (t *Libp2pTransport) Name() string { return "libp2p" }

// OpenStream implements Transport. Opens a new libp2p stream on the
// /bdpeer/1.0.0 protocol. The caller writes the frame payload and Close()s.
func (t *Libp2pTransport) OpenStream(ctx context.Context, p PeerID) (io.ReadWriteCloser, error) {
	pid, err := peer.Decode(string(p))
	if err != nil {
		return nil, fmt.Errorf("transport/libp2p: invalid peer id %q: %w", p, err)
	}
	s, err := t.host.Libp2p.NewStream(ctx, pid, proto.Protocol)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoConnection, err)
	}
	return s, nil
}

// SetHandler implements Transport. Registers the protocol stream handler.
// Must be called exactly once for the lifetime of the transport — subsequent
// calls are no-ops. This matches the libp2p model where the stream handler
// is set once at startup; concurrent runtime replacement is intentionally
// not supported.
func (t *Libp2pTransport) SetHandler(h Handler) {
	t.handOnce.Do(func() {
		t.host.Libp2p.SetStreamHandler(proto.Protocol, func(s network.Stream) {
			from := PeerID(s.Conn().RemotePeer().String())
			defer func() {
				if r := recover(); r != nil {
					_ = r // panic in handler must not kill transport goroutine
				}
			}()
			h(from, s)
		})
	})
}

// Close implements Transport. For libp2p this is a no-op — connection
// lifecycle is managed by the libp2p host itself.
func (t *Libp2pTransport) Close(_ PeerID) error { return nil }
