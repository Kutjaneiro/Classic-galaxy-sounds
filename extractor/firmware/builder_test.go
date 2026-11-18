package firmware

// Tiny ext2/ext4 and sparse image writers for the tests. They only produce
// what the reader needs (one block group, no bitmaps or checksums), so the
// tests run anywhere without root or e2fsprogs. The mke2fs tests also check
// the reader against real images when mke2fs is installed.

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"math/rand"
	pathpkg "path"
	"sort"
	"strings"
	"testing"
)

type buildOpts struct {
	blockSize   int
	inodeSize   int
	extents     bool // ext4 extent trees instead of ext2 block maps
	fragment    bool // leave a gap after every 2 data blocks (multiple extents)
	extentDepth int  // 1 = put the extents in a separate leaf block
	holes       bool // don't allocate all-zero blocks (sparse files)
}

func noise(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

var le = binary.LittleEndian

// buildExt builds an image containing files ({"dir/name": data}).
func buildExt(t testing.TB, files map[string][]byte, o buildOpts) []byte {
	t.Helper()
	if o.blockSize == 0 {
		o.blockSize = 1024
	}
	if o.inodeSize == 0 {
		o.inodeSize = 128
	}
	bs := o.blockSize
	fdb := 0
	if bs == 1024 {
		fdb = 1
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	dirs := map[string][]string{"": nil}
	for _, p := range paths {
		parts := strings.Split(p, "/")
		parent := ""
		for _, part := range parts[:len(parts)-1] {
			child := part
			if parent != "" {
				child = parent + "/" + part
			}
			if _, ok := dirs[child]; !ok {
				dirs[child] = nil
				dirs[parent] = append(dirs[parent], part)
			}
			parent = child
		}
		dirs[parent] = append(dirs[parent], parts[len(parts)-1])
	}
	dirKeys := make([]string, 0, len(dirs))
	for d := range dirs {
		dirKeys = append(dirKeys, d)
	}
	sort.Strings(dirKeys) // "" first

	ino := map[string]int{"": 2}
	next := 11
	for _, key := range append(dirKeys[1:], paths...) {
		ino[key] = next
		next++
	}

	perBlock := bs / o.inodeSize
	ninodes := (max(next-1, 16) + perBlock - 1) / perBlock * perBlock
	table := fdb + 4
	blocks := map[int][]byte{}
	nextBlock := table + ninodes/perBlock
	alloc := func() int { nextBlock++; return nextBlock - 1 }

	iBlockFor := func(pblocks []int) ([]byte, uint32) {
		iblock := make([]byte, 60)
		if o.extents {
			type run struct{ l, p, n int }
			var runs []run
			for l, p := range pblocks {
				if p == 0 {
					continue
				}
				if k := len(runs) - 1; k >= 0 && runs[k].l+runs[k].n == l && runs[k].p+runs[k].n == p {
					runs[k].n++
				} else {
					runs = append(runs, run{l, p, 1})
				}
			}
			var leaf []byte
			for _, r := range runs {
				e := make([]byte, 12)
				le.PutUint32(e, uint32(r.l))
				le.PutUint16(e[4:], uint16(r.n))
				le.PutUint16(e[6:], uint16(uint64(r.p)>>32))
				le.PutUint32(e[8:], uint32(r.p))
				leaf = append(leaf, e...)
			}
			header := func(entries, maxEntries, depth int) []byte {
				h := make([]byte, 12)
				le.PutUint16(h, extentMagic)
				le.PutUint16(h[2:], uint16(entries))
				le.PutUint16(h[4:], uint16(maxEntries))
				le.PutUint16(h[6:], uint16(depth))
				return h
			}
			if o.extentDepth == 0 {
				if len(runs) > 4 {
					t.Fatal("too many extents for depth 0")
				}
				copy(iblock, append(header(len(runs), 4, 0), leaf...))
				return iblock, extentsFl
			}
			lb := alloc()
			blk := make([]byte, bs)
			copy(blk, append(header(len(runs), (bs-12)/12, 0), leaf...))
			blocks[lb] = blk
			idx := make([]byte, 12)
			le.PutUint32(idx[4:], uint32(lb))
			copy(iblock, append(header(1, 4, 1), idx...))
			return iblock, extentsFl
		}

		per := bs / 4
		pointerBlock := func(entries []int) int {
			any := false
			for _, e := range entries {
				any = any || e != 0
			}
			if !any {
				return 0
			}
			b := alloc()
			blk := make([]byte, bs)
			for i, e := range entries {
				le.PutUint32(blk[4*i:], uint32(e))
			}
			blocks[b] = blk
			return b
		}
		ptrs := make([]int, 15)
		copy(ptrs, pblocks[:min(12, len(pblocks))])
		rest := pblocks[min(12, len(pblocks)):]
		if len(rest) > 0 {
			ptrs[12] = pointerBlock(rest[:min(per, len(rest))])
			rest = rest[min(per, len(rest)):]
		}
		var second []int
		for i := 0; i < min(len(rest), per*per); i += per {
			second = append(second, pointerBlock(rest[i:min(i+per, len(rest))]))
		}
		if len(second) > 0 {
			ptrs[13] = pointerBlock(second)
		}
		if len(rest) > per*per {
			t.Fatal("triple indirect not supported by the test builder")
		}
		for i, p := range ptrs {
			le.PutUint32(iblock[4*i:], uint32(p))
		}
		return iblock, 0
	}

	makeInode := func(mode uint16, data []byte, allowHoles bool) []byte {
		var pblocks []int
		for l := 0; l*bs < len(data); l++ {
			chunk := make([]byte, bs)
			copy(chunk, data[l*bs:min((l+1)*bs, len(data))])
			if allowHoles && bytes.Count(chunk, []byte{0}) == bs {
				pblocks = append(pblocks, 0)
				continue
			}
			p := alloc()
			blocks[p] = chunk
			pblocks = append(pblocks, p)
			if o.fragment && l%2 == 1 {
				alloc()
			}
		}
		iblock, flags := iBlockFor(pblocks)
		raw := make([]byte, o.inodeSize)
		le.PutUint16(raw, mode)
		le.PutUint32(raw[4:], uint32(len(data)))
		links := uint16(1)
		if mode&sIFDIR != 0 {
			links = 2
		}
		le.PutUint16(raw[26:], links)
		le.PutUint32(raw[32:], flags)
		copy(raw[40:100], iblock)
		le.PutUint32(raw[108:], uint32(uint64(len(data))>>32))
		return raw
	}

	dirData := func(p string) []byte {
		parent := ""
		if p != "" {
			if parent = pathpkg.Dir(p); parent == "." {
				parent = ""
			}
		}
		type entry struct {
			name     string
			num, typ int
		}
		entries := []entry{{".", ino[p], 2}, {"..", ino[parent], 2}}
		children := append([]string(nil), dirs[p]...)
		sort.Strings(children)
		for _, name := range children {
			child := name
			if p != "" {
				child = p + "/" + name
			}
			typ := 1
			if _, isDir := dirs[child]; isDir {
				typ = 2
			}
			entries = append(entries, entry{name, ino[child], typ})
		}
		var out, block []byte
		last := 0
		for _, e := range entries {
			size := (8 + len(e.name) + 3) &^ 3
			if len(block)+size > bs {
				le.PutUint16(block[last+4:], uint16(bs-last))
				out = append(out, append(block, make([]byte, bs-len(block))...)...)
				block = nil
			}
			last = len(block)
			rec := make([]byte, size)
			le.PutUint32(rec, uint32(e.num))
			le.PutUint16(rec[4:], uint16(size))
			rec[6] = byte(len(e.name))
			rec[7] = byte(e.typ)
			copy(rec[8:], e.name)
			block = append(block, rec...)
		}
		le.PutUint16(block[last+4:], uint16(bs-last))
		return append(out, append(block, make([]byte, bs-len(block))...)...)
	}

	inodes := map[int][]byte{}
	for _, d := range dirKeys {
		inodes[ino[d]] = makeInode(0x41ED, dirData(d), false)
	}
	for _, p := range paths {
		inodes[ino[p]] = makeInode(0x81A4, files[p], o.holes)
	}

	total := nextBlock + 1
	if total > 8*bs {
		t.Fatal("test image too big for a single block group")
	}
	img := make([]byte, total*bs)
	sb := img[1024:2048]
	le.PutUint32(sb[0:], uint32(ninodes))
	le.PutUint32(sb[4:], uint32(total))
	le.PutUint32(sb[20:], uint32(fdb))
	le.PutUint32(sb[24:], uint32(bits.Len(uint(bs))-11))
	le.PutUint32(sb[32:], uint32(8*bs))
	le.PutUint32(sb[40:], uint32(ninodes))
	le.PutUint16(sb[56:], 0xEF53)
	le.PutUint32(sb[76:], 1)
	le.PutUint32(sb[84:], 11)
	le.PutUint16(sb[88:], uint16(o.inodeSize))
	incompat := uint32(0x2) // filetype
	if o.extents {
		incompat |= 0x40
	}
	le.PutUint32(sb[96:], incompat)

	gdt := img[(fdb+1)*bs:]
	le.PutUint32(gdt[0:], uint32(fdb+2))
	le.PutUint32(gdt[4:], uint32(fdb+3))
	le.PutUint32(gdt[8:], uint32(table))
	for num, raw := range inodes {
		copy(img[table*bs+(num-1)*o.inodeSize:], raw)
	}
	for p, data := range blocks {
		copy(img[p*bs:], data)
	}
	return img
}

// toSparse encodes an image in Android sparse format using every chunk
// type. extraHeader pads both header sizes, like some Samsung images do.
func toSparse(raw []byte, bs, extraHeader int) []byte {
	if pad := (bs - len(raw)%bs) % bs; pad > 0 {
		raw = append(append([]byte(nil), raw...), make([]byte, pad)...)
	}
	type chunk struct {
		kind    uint16
		nblocks int
		payload []byte
	}
	var chunks []chunk
	for i := 0; i < len(raw)/bs; i++ {
		block := raw[i*bs : (i+1)*bs]
		kind, payload := uint16(chunkRaw), block
		switch {
		case bytes.Count(block, []byte{0}) == bs:
			kind, payload = chunkDontCare, nil
		case bytes.Equal(block, bytes.Repeat(block[:4], bs/4)):
			kind, payload = chunkFill, block[:4]
		}
		if k := len(chunks) - 1; k >= 0 && chunks[k].kind == kind &&
			(kind == chunkRaw || bytes.Equal(chunks[k].payload, payload)) {
			chunks[k].nblocks++
			if kind == chunkRaw {
				chunks[k].payload = append(chunks[k].payload, payload...)
			}
		} else {
			chunks = append(chunks, chunk{kind, 1, append([]byte(nil), payload...)})
		}
	}
	chunks = append(chunks, chunk{chunkCRC32, 0, make([]byte, 4)})

	chunkHdr := 12 + extraHeader
	h := make([]byte, 28+extraHeader)
	le.PutUint32(h, sparseMagic)
	le.PutUint16(h[4:], 1)
	le.PutUint16(h[8:], uint16(28+extraHeader))
	le.PutUint16(h[10:], uint16(chunkHdr))
	le.PutUint32(h[12:], uint32(bs))
	le.PutUint32(h[16:], uint32(len(raw)/bs))
	le.PutUint32(h[20:], uint32(len(chunks)))
	out := h
	for _, c := range chunks {
		ch := make([]byte, chunkHdr)
		le.PutUint16(ch, c.kind)
		le.PutUint32(ch[4:], uint32(c.nblocks))
		le.PutUint32(ch[8:], uint32(chunkHdr+len(c.payload)))
		out = append(out, ch...)
		out = append(out, c.payload...)
	}
	return out
}
