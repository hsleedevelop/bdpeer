package net

import (
	"bufio"
	"context"
	"fmt"

	"github.com/chad/bdpeer/internal/proto"
	"github.com/chad/bdpeer/internal/transfer"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// StartStreamHandler registers the /bdpeer/1.0.0 stream handler.
func (h *Host) StartStreamHandler() {
	h.Libp2p.SetStreamHandler(proto.Protocol, func(s network.Stream) {
		defer s.Close()
		r := bufio.NewReader(s)
		for {
			frame, err := transfer.ReadFrame(r)
			if err != nil {
				return
			}
			if frame.Type == proto.FrameFileStart {
				if h.OnFileStream != nil {
					h.OnFileStream(s.Conn().RemotePeer(), frame, r)
				}
				return
			}
			if h.OnFrame != nil {
				h.OnFrame(s.Conn().RemotePeer(), frame)
			}
		}
	})
}

// SendFrame opens a stream to peerID and sends one frame.
func (h *Host) SendFrame(ctx context.Context, peerID peer.ID, frame proto.Frame) error {
	s, err := h.Libp2p.NewStream(ctx, peerID, proto.Protocol)
	if err != nil {
		return fmt.Errorf("open stream to %s: %w", peerID, err)
	}
	defer s.Close()
	return transfer.WriteFrame(s, frame)
}
