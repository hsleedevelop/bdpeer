package discovery

const (
	bleChunkHdr  = 5
	bleChunkBody = 490
	bleDataType  = 'D'
)

// MakeDataChunks splits data into BLE chunk frames for DataChar transmission.
// Each chunk: [type(1)][idx_hi(1)][idx_lo(1)][total_hi(1)][total_lo(1)][payload...]
func MakeDataChunks(data []byte) [][]byte {
	total := (len(data) + bleChunkBody - 1) / bleChunkBody
	if total == 0 {
		total = 1
	}
	chunks := make([][]byte, total)
	for i := 0; i < total; i++ {
		offset := i * bleChunkBody
		end := offset + bleChunkBody
		if end > len(data) {
			end = len(data)
		}
		payload := data[offset:end]
		chunk := make([]byte, bleChunkHdr+len(payload))
		chunk[0] = bleDataType
		chunk[1] = byte(uint16(i) >> 8)
		chunk[2] = byte(uint16(i))
		chunk[3] = byte(uint16(total) >> 8)
		chunk[4] = byte(uint16(total))
		copy(chunk[bleChunkHdr:], payload)
		chunks[i] = chunk
	}
	return chunks
}

// ChunkAssembler reassembles BLE DataChar chunk sequences per peer.
// Not goroutine-safe — the caller must serialize Feed calls per instance.
type ChunkAssembler struct {
	bufs map[string][]byte
}

func NewChunkAssembler() *ChunkAssembler {
	return &ChunkAssembler{bufs: make(map[string][]byte)}
}

// Feed processes one raw BLE notification chunk for the given peer key.
// Returns (assembled data, true) when all chunks for a sequence have arrived.
func (a *ChunkAssembler) Feed(peerKey string, chunk []byte) ([]byte, bool) {
	if len(chunk) < bleChunkHdr {
		return nil, false
	}
	idx := uint16(chunk[1])<<8 | uint16(chunk[2])
	total := uint16(chunk[3])<<8 | uint16(chunk[4])
	payload := chunk[bleChunkHdr:]

	if idx == 0 {
		a.bufs[peerKey] = make([]byte, 0, int(total)*bleChunkBody)
	}
	a.bufs[peerKey] = append(a.bufs[peerKey], payload...)

	if idx == total-1 {
		result := a.bufs[peerKey]
		delete(a.bufs, peerKey)
		return result, true
	}
	return nil, false
}
