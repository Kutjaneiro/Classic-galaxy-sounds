package firmware

import (
	"encoding/binary"
	"sort"
)

// Android sparse image: a file header, then chunks that each describe a run
// of output blocks as raw data, a repeated 4-byte fill value, or "don't care"
// (zeros). Read in place through a chunk index, never expanded to disk.
const (
	sparseMagic   = 0xED26FF3A
	chunkRaw      = 0xCAC1
	chunkFill     = 0xCAC2
	chunkDontCare = 0xCAC3
	chunkCRC32    = 0xCAC4
)

type sparseChunk struct {
	start, blocks int64 // output blocks covered
	kind          uint16
	data          int64 // raw chunks: where the data starts in the source
	fill          [4]byte
}

// Sparse is a decoded view of an Android sparse image.
type Sparse struct {
	src       Source
	blockSize int64
	size      int64
	chunks    []sparseChunk
}

// IsSparse reports whether src starts with the sparse image magic.
func IsSparse(src Source) bool {
	if src.Size() < 28 {
		return false
	}
	b, err := readExact(src, 0, 4)
	return err == nil && binary.LittleEndian.Uint32(b) == sparseMagic
}

// OpenSparse indexes the chunks of a sparse image.
func OpenSparse(src Source) (*Sparse, error) {
	h, err := readExact(src, 0, 28)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	if le.Uint32(h) != sparseMagic {
		return nil, errorf("not an Android sparse image")
	}
	if major := le.Uint16(h[4:]); major != 1 {
		return nil, errorf("unsupported sparse image version %d", major)
	}
	// Header sizes come from the file: some Samsung images use larger ones
	fileHdr := int64(le.Uint16(h[8:]))
	chunkHdr := int64(le.Uint16(h[10:]))
	blockSize := int64(le.Uint32(h[12:]))
	totalBlocks := int64(le.Uint32(h[16:]))
	totalChunks := le.Uint32(h[20:])
	if fileHdr < 28 || chunkHdr < 12 || blockSize == 0 || blockSize%4 != 0 {
		return nil, errorf("corrupt sparse image header")
	}

	s := &Sparse{src: src, blockSize: blockSize, size: totalBlocks * blockSize}
	pos, block := fileHdr, int64(0)
	for i := uint32(0); i < totalChunks; i++ {
		c, err := readExact(src, pos, 12)
		if err != nil {
			return nil, err
		}
		kind := le.Uint16(c)
		nblocks := int64(le.Uint32(c[4:]))
		total := int64(le.Uint32(c[8:]))
		if total < chunkHdr {
			return nil, errorf("corrupt sparse image (bad chunk size)")
		}
		ch := sparseChunk{start: block, blocks: nblocks, kind: kind}
		switch kind {
		case chunkRaw:
			if total-chunkHdr != nblocks*blockSize {
				return nil, errorf("corrupt sparse image (bad raw chunk size)")
			}
			ch.data = pos + chunkHdr
		case chunkFill:
			f, err := readExact(src, pos+chunkHdr, 4)
			if err != nil {
				return nil, err
			}
			copy(ch.fill[:], f)
		case chunkDontCare:
		case chunkCRC32:
			pos += total
			continue
		default:
			return nil, errorf("corrupt sparse image (chunk type %#x)", kind)
		}
		s.chunks = append(s.chunks, ch)
		block += nblocks
		pos += total
	}
	return s, nil
}

// Size is the size of the expanded image.
func (s *Sparse) Size() int64 { return s.size }

// ReadAt reads the expanded image.
func (s *Sparse) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, errorf("image is truncated or corrupt (read past the end)")
	}
	done := 0
	for done < len(p) {
		cur := off + int64(done)
		want := int64(len(p) - done)
		blk := cur / s.blockSize
		i := sort.Search(len(s.chunks), func(i int) bool { return s.chunks[i].start > blk }) - 1

		var ch *sparseChunk
		var within, n int64
		if i >= 0 {
			ch = &s.chunks[i]
			within = cur - ch.start*s.blockSize
			n = min(want, ch.blocks*s.blockSize-within)
		}
		dst := p[done:]
		switch {
		case ch == nil || n <= 0: // past the last chunk: blocks never written are zeros
			n = want
			clear(dst[:n])
		case ch.kind == chunkRaw:
			if err := readInto(s.src, dst[:n], ch.data+within); err != nil {
				return done, err
			}
		case ch.kind == chunkFill:
			for k := int64(0); k < n; k++ {
				dst[k] = ch.fill[(within+k)%4]
			}
		default:
			clear(dst[:n])
		}
		done += int(n)
	}
	return len(p), nil
}
