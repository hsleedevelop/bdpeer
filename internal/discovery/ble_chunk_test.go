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

func TestChunkAssemblerOrphanedChunk(t *testing.T) {
	// If we receive a non-first chunk with no prior context, it must be dropped.
	a := NewChunkAssembler()
	data := make([]byte, 1000) // 3 chunks
	chunks := MakeDataChunks(data)

	// Skip chunk 0, feed chunk 1 directly.
	result, done := a.Feed("peer1", chunks[1])
	if done || result != nil {
		t.Fatal("orphaned non-first chunk should be dropped, got result")
	}
	// Feed chunk 2 (last) — should also be dropped (no context).
	result, done = a.Feed("peer1", chunks[2])
	if done || result != nil {
		t.Fatal("orphaned last chunk should be dropped, got result")
	}
}

func TestChunkAssemblerDropsGapBeforeLastChunk(t *testing.T) {
	a := NewChunkAssembler()
	data := make([]byte, 1000) // 3 chunks
	chunks := MakeDataChunks(data)

	if result, done := a.Feed("peer1", chunks[0]); done || result != nil {
		t.Fatal("first chunk should only start assembly")
	}
	result, done := a.Feed("peer1", chunks[2])
	if done || result != nil {
		t.Fatal("gapped last chunk should be dropped, got result")
	}
	result, done = a.Feed("peer1", chunks[1])
	if done || result != nil {
		t.Fatal("sequence should reset after a gap")
	}
}

func TestChunkAssemblerTotalZero(t *testing.T) {
	a := NewChunkAssembler()
	// Craft a malformed chunk with total==0.
	malformed := []byte{'D', 0, 0, 0, 0, 'x'} // type, idx=0, total=0, payload
	result, done := a.Feed("peer1", malformed)
	if done || result != nil {
		t.Fatal("chunk with total==0 should be rejected")
	}
}

func TestChunkAssemblerExactBoundary(t *testing.T) {
	// Exactly 490 bytes should produce 1 chunk.
	data := make([]byte, bleChunkBody)
	for i := range data {
		data[i] = byte(i % 256)
	}
	chunks := MakeDataChunks(data)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk for exactly bleChunkBody bytes, got %d", len(chunks))
	}
	a := NewChunkAssembler()
	result, done := a.Feed("peer1", chunks[0])
	if !done {
		t.Fatal("single exact-boundary chunk should be done immediately")
	}
	if len(result) != bleChunkBody {
		t.Fatalf("want %d bytes, got %d", bleChunkBody, len(result))
	}
}

func TestChunkAssemblerReset(t *testing.T) {
	a := NewChunkAssembler()
	data := make([]byte, 1000)
	chunks := MakeDataChunks(data)

	// Start a sequence (feed chunk 0).
	_, done := a.Feed("peer1", chunks[0])
	if done {
		t.Fatal("multi-chunk sequence should not be done after first chunk")
	}

	// Reset — discard partial state.
	a.Reset("peer1")

	// Now feed chunk 0 again and complete the sequence — should work.
	a.Feed("peer1", chunks[0])
	_, done = a.Feed("peer1", chunks[1])
	if done {
		t.Fatal("sequence not complete after 2 of 3 chunks")
	}
	result, done := a.Feed("peer1", chunks[2])
	if !done || len(result) != 1000 {
		t.Fatalf("want done=true and 1000 bytes after reset, got done=%v len=%d", done, len(result))
	}
}
