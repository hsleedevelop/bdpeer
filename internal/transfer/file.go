package transfer

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chad/bdpeer/internal/proto"
)

// WriteFile sends FILE_START, FILE_CHUNK..., FILE_END frames to w.
func WriteFile(srcPath, from string, w io.Writer) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	if err := WriteFrame(w, proto.Frame{
		Type: proto.FrameFileStart,
		From: from,
		Name: filepath.Base(srcPath),
		Size: info.Size(),
	}); err != nil {
		return err
	}

	h := sha256.New()
	buf := make([]byte, proto.ChunkSize)
	seq := 0
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			h.Write(chunk)
			if err := WriteFrame(w, proto.Frame{
				Type: proto.FrameFileChunk,
				Seq:  seq,
				Data: chunk,
			}); err != nil {
				return err
			}
			seq++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return WriteFrame(w, proto.Frame{
		Type:     proto.FrameFileEnd,
		Checksum: fmt.Sprintf("sha256:%x", h.Sum(nil)),
	})
}

// ReadFile reads FILE_START, FILE_CHUNK..., FILE_END frames from r into destDir.
// progress is called after each chunk. Returns the path of the written file.
func ReadFile(r io.Reader, destDir string, progress func(int64, int64)) (string, error) {
	startFrame, err := ReadFrame(r)
	if err != nil {
		return "", fmt.Errorf("read FILE_START: %w", err)
	}
	if startFrame.Type != proto.FrameFileStart {
		return "", fmt.Errorf("expected FILE_START, got %s", startFrame.Type)
	}

	destPath := filepath.Join(destDir, filepath.Base(startFrame.Name))
	f, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	var received int64
	for {
		frame, err := ReadFrame(r)
		if err != nil {
			return "", fmt.Errorf("read chunk: %w", err)
		}
		if frame.Type == proto.FrameFileEnd {
			expected := fmt.Sprintf("sha256:%x", h.Sum(nil))
			if frame.Checksum != expected {
				os.Remove(destPath)
				return "", fmt.Errorf("checksum mismatch: got %s, want %s", frame.Checksum, expected)
			}
			return destPath, nil
		}
		if frame.Type != proto.FrameFileChunk {
			return "", fmt.Errorf("expected FILE_CHUNK, got %s", frame.Type)
		}
		if _, err := f.Write(frame.Data); err != nil {
			return "", err
		}
		h.Write(frame.Data)
		received += int64(len(frame.Data))
		if progress != nil {
			progress(received, startFrame.Size)
		}
	}
}
