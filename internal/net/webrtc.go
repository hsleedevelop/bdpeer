package net

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/pion/webrtc/v4"
)

// stunServers are free public STUN servers for ICE NAT traversal.
var stunServers = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun1.l.google.com:19302",
}

// WebRTCConn wraps a pion PeerConnection with a reliable data channel.
// It implements io.ReadWriteCloser so it can carry bdpeer protocol frames.
type WebRTCConn struct {
	pc      *webrtc.PeerConnection
	dc      *webrtc.DataChannel
	readBuf chan []byte
	once    sync.Once
	closed  chan struct{}
}

func newWebRTCConfig() webrtc.Configuration {
	servers := make([]webrtc.ICEServer, 0, len(stunServers))
	for _, url := range stunServers {
		servers = append(servers, webrtc.ICEServer{URLs: []string{url}})
	}
	return webrtc.Configuration{ICEServers: servers}
}

// NewWebRTCOffer creates a peer connection and returns an SDP offer string.
// Call SetAnswer with the remote answer to complete the handshake.
func NewWebRTCOffer(ctx context.Context) (*WebRTCConn, string, error) {
	pc, err := webrtc.NewPeerConnection(newWebRTCConfig())
	if err != nil {
		return nil, "", fmt.Errorf("new peer connection: %w", err)
	}

	conn := &WebRTCConn{pc: pc, readBuf: make(chan []byte, 64), closed: make(chan struct{})}

	dc, err := pc.CreateDataChannel("bdpeer", nil)
	if err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("create data channel: %w", err)
	}
	conn.dc = dc
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		select {
		case conn.readBuf <- msg.Data:
		default:
		}
	})
	dc.OnClose(func() { conn.once.Do(func() { close(conn.closed) }) })

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("create offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, "", fmt.Errorf("set local description: %w", err)
	}

	// Wait for ICE gathering to complete (or context cancel).
	gathering := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gathering:
	case <-ctx.Done():
		pc.Close()
		return nil, "", ctx.Err()
	}

	return conn, pc.LocalDescription().SDP, nil
}

// NewWebRTCAnswer creates a peer connection from a remote offer and returns an SDP answer.
func NewWebRTCAnswer(ctx context.Context, offerSDP string) (*WebRTCConn, string, error) {
	pc, err := webrtc.NewPeerConnection(newWebRTCConfig())
	if err != nil {
		return nil, "", fmt.Errorf("new peer connection: %w", err)
	}

	conn := &WebRTCConn{pc: pc, readBuf: make(chan []byte, 64), closed: make(chan struct{})}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		conn.dc = dc
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			select {
			case conn.readBuf <- msg.Data:
			default:
			}
		})
		dc.OnClose(func() { conn.once.Do(func() { close(conn.closed) }) })
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

	gathering := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gathering:
	case <-ctx.Done():
		pc.Close()
		return nil, "", ctx.Err()
	}

	return conn, pc.LocalDescription().SDP, nil
}

// SetAnswer completes the offer side handshake with the remote answer SDP.
func (c *WebRTCConn) SetAnswer(answerSDP string) error {
	return c.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: answerSDP,
	})
}

func (c *WebRTCConn) Read(p []byte) (int, error) {
	select {
	case data, ok := <-c.readBuf:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, data)
		return n, nil
	case <-c.closed:
		return 0, io.EOF
	}
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
	c.once.Do(func() { close(c.closed) })
	return c.pc.Close()
}

// Connected returns true once ICE connection is established.
func (c *WebRTCConn) Connected() <-chan struct{} {
	ch := make(chan struct{})
	c.pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			close(ch)
		}
	})
	return ch
}
