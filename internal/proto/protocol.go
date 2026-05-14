package proto

const Protocol = "/bdpeer/1.0.0"
const ChunkSize = 32 * 1024 // 32 KB

type FrameType string

const (
	FrameText      FrameType = "text"
	FrameFileStart FrameType = "file_start"
	FrameFileChunk FrameType = "file_chunk"
	FrameFileEnd   FrameType = "file_end"
	FrameHello     FrameType = "hello"
)

type Frame struct {
	Type     FrameType `json:"type"`
	From     string    `json:"from"`
	Content  string    `json:"content,omitempty"`
	Name     string    `json:"name,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Seq      int       `json:"seq,omitempty"`
	Data     []byte    `json:"data,omitempty"`
	Checksum string    `json:"checksum,omitempty"`
}
