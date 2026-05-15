package discovery

import (
	"testing"
)

func TestMakeAndAssembleChunks(t *testing.T) {
	data := make([]byte, 1100) // spans 3 chunks (490+490+120)
	for i := range data {
		data[i] = byte(i % 256)
	}

	chunks := MakeDataChunks(data)
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(chunks))
	}

	a := NewChunkAssembler()
	var result []byte
	for _, c := range chunks {
		if got, done := a.Feed("peer1", c); done {
			result = got
		}
	}
	if len(result) != len(data) {
		t.Fatalf("want %d bytes, got %d", len(data), len(result))
	}
	for i, b := range result {
		if b != data[i] {
			t.Fatalf("byte %d: want %d, got %d", i, data[i], b)
		}
	}
}

func TestChunkAssemblerSingleChunk(t *testing.T) {
	data := []byte("hello world")
	chunks := MakeDataChunks(data)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}

	a := NewChunkAssembler()
	result, done := a.Feed("peer1", chunks[0])
	if !done {
		t.Fatal("single chunk should be done immediately")
	}
	if string(result) != "hello world" {
		t.Fatalf("want 'hello world', got %q", result)
	}
}

func TestChunkAssemblerMultiplePeers(t *testing.T) {
	dataA := []byte("message from A")
	dataB := []byte("message from B")
	chunksA := MakeDataChunks(dataA)
	chunksB := MakeDataChunks(dataB)

	a := NewChunkAssembler()
	resA, doneA := a.Feed("peerA", chunksA[0])
	resB, doneB := a.Feed("peerB", chunksB[0])
	if !doneA || !doneB {
		t.Fatal("single-chunk messages should be done immediately")
	}
	if string(resA) != "message from A" || string(resB) != "message from B" {
		t.Fatalf("peer isolation broken: %q %q", resA, resB)
	}
}
