package net_test

import (
	"context"
	"testing"

	bnet "github.com/hsleedevelop/bdpeer/internal/net"
)

func TestNewWebRTCOfferReturnsSDPWhenGatheringContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, sdp, err := bnet.NewWebRTCOffer(ctx, nil)
	if err != nil {
		t.Fatalf("NewWebRTCOffer: %v", err)
	}
	defer conn.Close()

	if sdp == "" {
		t.Fatal("expected SDP offer")
	}
}

func TestNewWebRTCAnswerReturnsSDPWhenGatheringContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	offerConn, offerSDP, err := bnet.NewWebRTCOffer(ctx, nil)
	if err != nil {
		t.Fatalf("NewWebRTCOffer: %v", err)
	}
	defer offerConn.Close()

	answerConn, answerSDP, err := bnet.NewWebRTCAnswer(ctx, offerSDP, nil)
	if err != nil {
		t.Fatalf("NewWebRTCAnswer: %v", err)
	}
	defer answerConn.Close()

	if answerSDP == "" {
		t.Fatal("expected SDP answer")
	}
}
