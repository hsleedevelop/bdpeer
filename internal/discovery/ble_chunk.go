package discovery

const (
	bleChunkHdr         = 5
	bleSessionChunkMeta = 2
	bleChunkBody        = 490
	bleDataType         = 'D'
	bleSessionDataType  = 'S'
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
	states map[string]*chunkState
}

type chunkState struct {
	buf   []byte
	next  uint16
	total uint16
}

func NewChunkAssembler() *ChunkAssembler {
	return &ChunkAssembler{states: make(map[string]*chunkState)}
}

// Feed processes one raw BLE notification chunk for the given peer key.
// Returns (assembled data, true) when all chunks for a sequence have arrived.
func (a *ChunkAssembler) Feed(peerKey string, chunk []byte) ([]byte, bool) {
	if len(chunk) < bleChunkHdr {
		return nil, false
	}
	if chunk[0] != bleDataType {
		return nil, false
	}
	idx := uint16(chunk[1])<<8 | uint16(chunk[2])
	total := uint16(chunk[3])<<8 | uint16(chunk[4])

	if total == 0 {
		return nil, false
	}
	if idx >= total {
		a.Reset(peerKey)
		return nil, false
	}

	payload := chunk[bleChunkHdr:]

	if idx == 0 {
		a.states[peerKey] = &chunkState{
			buf:   make([]byte, 0, int(total)*bleChunkBody),
			next:  1,
			total: total,
		}
	} else {
		st, ok := a.states[peerKey]
		if !ok || idx != st.next || total != st.total {
			a.Reset(peerKey)
			return nil, false
		}
		st.next++
	}

	st := a.states[peerKey]
	st.buf = append(st.buf, payload...)

	if idx == total-1 {
		result := st.buf
		delete(a.states, peerKey)
		return result, true
	}
	return nil, false
}

// Reset discards any partial assembly state for peerKey.
// Call when a peer disconnects to prevent stale buffer accumulation.
func (a *ChunkAssembler) Reset(peerKey string) {
	delete(a.states, peerKey)
}

// MakeSessionDataChunks splits data into BLE chunk frames carrying explicit
// sender and recipient session IDs. This lets platforms that can only
// broadcast notifications filter messages locally.
func MakeSessionDataChunks(fromSession, toSession string, data []byte) [][]byte {
	from := []byte(fromSession)
	to := []byte(toSession)
	if len(from) > 255 || len(to) > 255 {
		return nil
	}
	payloadLimit := bleChunkBody - bleSessionChunkMeta - len(from) - len(to)
	if payloadLimit <= 0 {
		return nil
	}
	total := (len(data) + payloadLimit - 1) / payloadLimit
	if total == 0 {
		total = 1
	}
	chunks := make([][]byte, total)
	for i := 0; i < total; i++ {
		offset := i * payloadLimit
		end := offset + payloadLimit
		if end > len(data) {
			end = len(data)
		}
		payload := data[offset:end]
		chunk := make([]byte, bleChunkHdr+bleSessionChunkMeta+len(from)+len(to)+len(payload))
		chunk[0] = bleSessionDataType
		chunk[1] = byte(uint16(i) >> 8)
		chunk[2] = byte(uint16(i))
		chunk[3] = byte(uint16(total) >> 8)
		chunk[4] = byte(uint16(total))
		chunk[5] = byte(len(from))
		chunk[6] = byte(len(to))
		pos := bleChunkHdr + bleSessionChunkMeta
		copy(chunk[pos:], from)
		pos += len(from)
		copy(chunk[pos:], to)
		pos += len(to)
		copy(chunk[pos:], payload)
		chunks[i] = chunk
	}
	return chunks
}

// SessionChunkAssembler reassembles session-addressed BLE chunks by sender.
// Not goroutine-safe — the caller must serialize Feed calls per instance.
type SessionChunkAssembler struct {
	states map[string]*chunkState
}

func NewSessionChunkAssembler() *SessionChunkAssembler {
	return &SessionChunkAssembler{states: make(map[string]*chunkState)}
}

// Feed processes one raw session BLE chunk. It returns the sender session ID,
// assembled data, and done=true only when the chunk targets localSession and a
// full message has been reassembled.
func (a *SessionChunkAssembler) Feed(localSession string, chunk []byte) (string, []byte, bool) {
	from, to, idx, total, payload, ok := parseSessionChunk(chunk)
	if !ok {
		return "", nil, false
	}
	if to != localSession {
		return "", nil, false
	}
	if total == 0 {
		return "", nil, false
	}
	if idx >= total {
		a.Reset(from)
		return "", nil, false
	}
	if idx == 0 {
		a.states[from] = &chunkState{
			buf:   make([]byte, 0, int(total)*bleChunkBody),
			next:  1,
			total: total,
		}
	} else {
		st, ok := a.states[from]
		if !ok || idx != st.next || total != st.total {
			a.Reset(from)
			return "", nil, false
		}
		st.next++
	}
	st := a.states[from]
	st.buf = append(st.buf, payload...)
	if idx == total-1 {
		result := st.buf
		delete(a.states, from)
		return from, result, true
	}
	return "", nil, false
}

func (a *SessionChunkAssembler) Reset(sessionID string) {
	delete(a.states, sessionID)
}

func parseSessionChunk(chunk []byte) (from, to string, idx, total uint16, payload []byte, ok bool) {
	if len(chunk) < bleChunkHdr+bleSessionChunkMeta {
		return "", "", 0, 0, nil, false
	}
	if chunk[0] != bleSessionDataType {
		return "", "", 0, 0, nil, false
	}
	idx = uint16(chunk[1])<<8 | uint16(chunk[2])
	total = uint16(chunk[3])<<8 | uint16(chunk[4])
	fromLen := int(chunk[5])
	toLen := int(chunk[6])
	metaLen := bleChunkHdr + bleSessionChunkMeta + fromLen + toLen
	if total == 0 || fromLen == 0 || toLen == 0 || len(chunk) < metaLen {
		return "", "", 0, 0, nil, false
	}
	pos := bleChunkHdr + bleSessionChunkMeta
	from = string(chunk[pos : pos+fromLen])
	pos += fromLen
	to = string(chunk[pos : pos+toLen])
	pos += toLen
	payload = chunk[pos:]
	return from, to, idx, total, payload, true
}
