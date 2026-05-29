package transfer_test

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/hsleedevelop/bdpeer/internal/transfer"
)

func TestFileSendReceive(t *testing.T) {
	data := make([]byte, 100*1024)
	rand.Read(data)
	srcPath := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(srcPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := transfer.WriteFile(srcPath, "alice", "tid-test", &buf, nil); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	destDir := t.TempDir()
	progress := make([]int64, 0)
	dest, err := transfer.ReadFile(&buf, destDir, func(recv, total int64) {
		progress = append(progress, recv)
	})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Error("received file content does not match source")
	}
	if len(progress) == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestFileSendReceiveWithCustomChunkSize(t *testing.T) {
	data := make([]byte, 9*1024)
	rand.Read(data)
	srcPath := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(srcPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := transfer.WriteFileWithChunkSize(srcPath, "alice", "tid-test", &buf, 2*1024, nil); err != nil {
		t.Fatalf("WriteFileWithChunkSize: %v", err)
	}

	dest, err := transfer.ReadFile(&buf, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Error("received file content does not match source")
	}
}
