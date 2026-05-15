package net

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hsleedevelop/bdpeer/internal/config"
	"github.com/pion/webrtc/v4"
)

// WebRTCConn wraps a pion PeerConnection with a reliable data channel.
// It implements io.ReadWriteCloser so it can carry bdpeer protocol frames.
// io.Pipe is used for the read path so transfer.ReadFrame (io.ReadFull) works correctly.
type WebRTCConn struct {
	pc          *webrtc.PeerConnection
	dc          *webrtc.DataChannel
	pr          *io.PipeReader
	pw          *io.PipeWriter
	once        sync.Once
	dcOnce      sync.Once
	connectedCh chan struct{}
	dcReadyCh   chan struct{}
}

// newWebRTCConfig builds a WebRTC configuration with STUN + TURN servers.
// turnServers come from config — custom servers override the built-in Open Relay defaults.
func newWebRTCConfig(turnServers []config.TURNServer) webrtc.Configuration {
	servers := []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"stun:stun1.l.google.com:19302"}},
	}
	for _, t := range turnServers {
		servers = append(servers, webrtc.ICEServer{
			URLs:           []string{t.URL},
			Username:       t.Username,
			Credential:     t.Credential,
			CredentialType: webrtc.ICECredentialTypePassword,
		})
	}
	return webrtc.Configuration{ICEServers: servers}
}

func newWebRTCConn(pc *webrtc.PeerConnection) *WebRTCConn {
	pr, pw := io.Pipe()
	c := &WebRTCConn{
		pc:          pc,
		pr:          pr,
		pw:          pw,
		connectedCh: make(chan struct{}),
		dcReadyCh:   make(chan struct{}),
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateConnected:
			c.once.Do(func() { close(c.connectedCh) })
		case webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateDisconnected,
			webrtc.PeerConnectionStateClosed:
			pw.CloseWithError(io.ErrClosedPipe)
		}
	})
	return c
}

func waitForICEGathering(ctx context.Context, pc *webrtc.PeerConnection) (string, error) {
	gathering := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gathering:
	case <-ctx.Done():
		// Keep the handshake moving with candidates gathered so far. Some
		// networks block STUN/TURN lookups long enough to hit our deadline; in
		// that case dropping the SDP prevents BLE signalling from ever starting.
		// Do not close the peer connection here: the caller still needs it to
		// complete the handshake with this partial SDP, and later timeout paths
		// close the returned WebRTCConn if connection establishment fails.
	}

	desc := pc.LocalDescription()
	if desc == nil || desc.SDP == "" {
		return "", errors.New("no local description after ICE gathering")
	}
	return desc.SDP, nil
}

func (c *WebRTCConn) wireDataChannel(dc *webrtc.DataChannel) {
	c.dc = dc
	dc.OnOpen(func() {
		c.dcOnce.Do(func() { close(c.dcReadyCh) })
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Write each message into the pipe; io.ReadFull on the other end assembles frames.
		c.pw.Write(msg.Data) //nolint:errcheck
	})
	dc.OnClose(func() {
		c.pw.CloseWithError(io.EOF)
	})
}

// DataChannelOpen returns a channel that closes when the data channel is open
// and ready for writes. This fires after ICE + DTLS + SCTP handshakes complete —
// i.e., strictly after Connected(). Callers that write to the conn must wait on
// this channel before attempting writes to avoid io.ErrClosedPipe.
func (c *WebRTCConn) DataChannelOpen() <-chan struct{} {
	return c.dcReadyCh
}

// NewWebRTCOffer creates a peer connection and returns an SDP offer string.
// Call SetAnswer with the remote answer to complete the handshake.
func NewWebRTCOffer(ctx context.Context, turnServers []config.TURNServer) (*WebRTCConn, string, error) {
	pc, err := webrtc.NewPeerConnection(newWebRTCConfig(turnServers))
	if err != nil {
		return nil, "", fmt.Errorf("new peer connection: %w", err)
	}

	conn := newWebRTCConn(pc)

	dc, err := pc.CreateDataChannel("bdpeer", nil)
	if err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("create data channel: %w", err)
	}
	conn.wireDataChannel(dc)

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("create offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("set local description: %w", err)
	}

	offerSDP, err := waitForICEGathering(ctx, pc)
	if err != nil {
		go pc.Close()
		return nil, "", err
	}

	return conn, offerSDP, nil
}

// NewWebRTCAnswer creates a peer connection from a remote offer and returns an SDP answer.
func NewWebRTCAnswer(ctx context.Context, offerSDP string, turnServers []config.TURNServer) (*WebRTCConn, string, error) {
	pc, err := webrtc.NewPeerConnection(newWebRTCConfig(turnServers))
	if err != nil {
		return nil, "", fmt.Errorf("new peer connection: %w", err)
	}

	conn := newWebRTCConn(pc)

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		conn.wireDataChannel(dc)
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: offerSDP,
	}); err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("create answer: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("set local description: %w", err)
	}

	answerSDP, err := waitForICEGathering(ctx, pc)
	if err != nil {
		go pc.Close()
		return nil, "", err
	}

	return conn, answerSDP, nil
}

// SetAnswer completes the offer side handshake with the remote answer SDP.
func (c *WebRTCConn) SetAnswer(answerSDP string) error {
	return c.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: answerSDP,
	})
}

// Connected returns a channel that closes when the ICE connection is established.
func (c *WebRTCConn) Connected() <-chan struct{} {
	return c.connectedCh
}

func (c *WebRTCConn) Read(p []byte) (int, error) {
	return c.pr.Read(p)
}

func (c *WebRTCConn) Write(p []byte) (int, error) {
	if c.dc == nil {
		return 0, fmt.Errorf("data channel not ready")
	}
	if err := c.dc.Send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *WebRTCConn) Close() error {
	c.pw.CloseWithError(io.ErrClosedPipe)
	return c.pc.Close()
}
