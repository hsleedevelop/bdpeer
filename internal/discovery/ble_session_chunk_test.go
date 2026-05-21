package discovery

import "testing"

func TestMakeAndAssembleSessionChunks(t *testing.T) {
	data := make([]byte, 1400)
	for i := range data {
		data[i] = byte(i % 251)
	}

	chunks := MakeSessionDataChunks("from-session", "to-session", data)
	if len(chunks) < 2 {
		t.Fatalf("want multiple chunks, got %d", len(chunks))
	}

	a := NewSessionChunkAssembler()
	var (
		from   string
		result []byte
	)
	for _, c := range chunks {
		if gotFrom, got, done := a.Feed("to-session", c); done {
			from = gotFrom
			result = got
		}
	}
	if from != "from-session" {
		t.Fatalf("from=%q, want from-session", from)
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

func TestSessionChunkAssemblerFiltersOtherRecipients(t *testing.T) {
	chunks := MakeSessionDataChunks("alice", "bob", []byte("hello"))
	a := NewSessionChunkAssembler()
	from, result, done := a.Feed("carol", chunks[0])
	if done || from != "" || result != nil {
		t.Fatalf("chunk for another recipient should be ignored, from=%q done=%v", from, done)
	}
}

func TestSessionChunkAssemblerMultipleSenders(t *testing.T) {
	chunksA := MakeSessionDataChunks("alice", "me", []byte("from alice"))
	chunksB := MakeSessionDataChunks("bob", "me", []byte("from bob"))
	a := NewSessionChunkAssembler()

	fromA, resultA, doneA := a.Feed("me", chunksA[0])
	fromB, resultB, doneB := a.Feed("me", chunksB[0])
	if !doneA || !doneB {
		t.Fatal("single chunk session messages should complete immediately")
	}
	if fromA != "alice" || string(resultA) != "from alice" {
		t.Fatalf("alice result mismatch: %q %q", fromA, resultA)
	}
	if fromB != "bob" || string(resultB) != "from bob" {
		t.Fatalf("bob result mismatch: %q %q", fromB, resultB)
	}
}

func TestMakeSessionDataChunksRejectsOversizedSessionIDs(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'x'
	}
	if chunks := MakeSessionDataChunks(string(long), "to", []byte("x")); chunks != nil {
		t.Fatalf("oversized from session should be rejected")
	}
	if chunks := MakeSessionDataChunks("from", string(long), []byte("x")); chunks != nil {
		t.Fatalf("oversized to session should be rejected")
	}
}
