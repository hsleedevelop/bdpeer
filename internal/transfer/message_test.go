package transfer_test

import (
	"bytes"
	"testing"

	"github.com/chad/bdpeer/internal/proto"
	"github.com/chad/bdpeer/internal/transfer"
)

func TestWriteReadTextFrame(t *testing.T) {
	buf := &bytes.Buffer{}
	frame := proto.Frame{
		Type:    proto.FrameText,
		From:    "alice",
		Content: "hello bob",
	}

	if err := transfer.WriteFrame(buf, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := transfer.ReadFrame(buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Content != "hello bob" {
		t.Errorf("got %q, want %q", got.Content, "hello bob")
	}
	if got.From != "alice" {
		t.Errorf("got from %q, want alice", got.From)
	}
}

func TestWriteReadMultipleFrames(t *testing.T) {
	buf := &bytes.Buffer{}
	frames := []proto.Frame{
		{Type: proto.FrameText, From: "a", Content: "one"},
		{Type: proto.FrameText, From: "b", Content: "two"},
	}
	for _, f := range frames {
		if err := transfer.WriteFrame(buf, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range frames {
		got, err := transfer.ReadFrame(buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.Content != want.Content {
			t.Errorf("got %q, want %q", got.Content, want.Content)
		}
	}
}
