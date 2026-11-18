package firmware

import (
	"encoding/binary"
	"strings"
)

// Ext4 is a read-only ext2/3/4 reader: enough to look up paths, list
// directories and read files. It handles block maps (ext2/3, direct and
// indirect pointers) and extent trees (ext4), 32 and 64-bit group
// descriptors, holes and unwritten extents.
type Ext4 struct {
	src            Source
	blockSize      int64
	firstDataBlock int64
	inodesPerGroup uint32
	inodeSize      int64
	descSize       int64
}

// Inode is the part of an on-disk inode the reader needs.
type Inode struct {
	Mode  uint16
	Size  uint64
	Flags uint32
	block [60]byte // i_block: block map or extent tree root
}

const (
	sIFMT        = 0xF000
	sIFDIR       = 0x4000
	sIFREG       = 0x8000
	extentsFl    = 0x80000
	inlineDataFl = 0x10000000
	extentMagic  = 0xF30A
	erofsMagic   = 0xE0F5E1E2
	rootInode    = 2
	// Sounds and build.prop are small; refuse absurd sizes from corrupt inodes
	maxFileSize = 512 << 20
)

// IsDir reports whether the inode is a directory.
func (i Inode) IsDir() bool { return i.Mode&sIFMT == sIFDIR }

// IsFile reports whether the inode is a regular file.
func (i Inode) IsFile() bool { return i.Mode&sIFMT == sIFREG }

// OpenExt4 reads the superblock.
func OpenExt4(src Source) (*Ext4, error) {
	if src.Size() < 2048 {
		return nil, errorf("not an ext2/ext3/ext4 image")
	}
	sb, err := readExact(src, 1024, 1024)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	if le.Uint32(sb) == erofsMagic {
		return nil, errNewFirmware("this system image (EROFS)")
	}
	if le.Uint16(sb[56:]) != 0xEF53 {
		return nil, errorf("not an ext2/ext3/ext4 image")
	}
	logBlock := le.Uint32(sb[24:])
	if logBlock > 6 {
		return nil, errorf("corrupt ext4 superblock (block size)")
	}
	fs := &Ext4{
		src:            src,
		blockSize:      1024 << logBlock,
		firstDataBlock: int64(le.Uint32(sb[20:])),
		inodesPerGroup: le.Uint32(sb[40:]),
		inodeSize:      128,
		descSize:       32,
	}
	if fs.inodesPerGroup == 0 {
		return nil, errorf("corrupt ext4 superblock (inodes per group)")
	}
	if rev := le.Uint32(sb[76:]); rev >= 1 {
		fs.inodeSize = int64(le.Uint16(sb[88:]))
	}
	if fs.inodeSize < 128 {
		return nil, errorf("corrupt ext4 superblock (inode size)")
	}
	incompat := le.Uint32(sb[96:])
	if d := int64(le.Uint16(sb[254:])); incompat&0x80 != 0 && d != 0 { // 64bit
		fs.descSize = d
	}
	for flag, name := range map[uint32]string{
		0x1:     "compression",
		0x8:     "external journal device",
		0x10:    "meta_bg",
		0x10000: "encryption",
	} {
		if incompat&flag != 0 {
			return nil, errorf("unsupported ext4 feature: %s", name)
		}
	}
	return fs, nil
}

func (fs *Ext4) blocks(n, count int64) ([]byte, error) {
	if n < 0 || count < 0 || count > fs.src.Size()/fs.blockSize {
		return nil, errorf("corrupt ext4 image (bad block range)")
	}
	return readExact(fs.src, n*fs.blockSize, int(count*fs.blockSize))
}

// Inode reads inode number n.
func (fs *Ext4) Inode(n uint32) (Inode, error) {
	if n == 0 {
		return Inode{}, errorf("corrupt ext4 image (inode 0)")
	}
	le := binary.LittleEndian
	group := int64((n - 1) / fs.inodesPerGroup)
	index := int64((n - 1) % fs.inodesPerGroup)

	gdt := (fs.firstDataBlock + 1) * fs.blockSize
	desc, err := readExact(fs.src, gdt+group*fs.descSize, int(fs.descSize))
	if err != nil {
		return Inode{}, err
	}
	table := int64(le.Uint32(desc[8:]))
	if fs.descSize >= 64 {
		table |= int64(le.Uint32(desc[40:])) << 32
	}
	raw, err := readExact(fs.src, table*fs.blockSize+index*fs.inodeSize, 128)
	if err != nil {
		return Inode{}, err
	}
	ino := Inode{
		Mode:  le.Uint16(raw[0:]),
		Size:  uint64(le.Uint32(raw[4:])) | uint64(le.Uint32(raw[108:]))<<32,
		Flags: le.Uint32(raw[32:]),
	}
	copy(ino.block[:], raw[40:100])
	return ino, nil
}

// extents calls fn for every extent: logical block, physical block, length,
// and whether it is initialized (unwritten extents read as zeros).
func (fs *Ext4) extents(node []byte, depth int, fn func(l uint32, p uint64, n uint32, init bool) error) error {
	le := binary.LittleEndian
	if len(node) < 12 || le.Uint16(node) != extentMagic || depth > 5 {
		return errorf("corrupt ext4 extent tree")
	}
	entries := int(le.Uint16(node[2:]))
	level := le.Uint16(node[6:])
	if 12+12*entries > len(node) {
		return errorf("corrupt ext4 extent tree")
	}
	for k := 0; k < entries; k++ {
		e := node[12+12*k:]
		if level == 0 {
			lblock := le.Uint32(e)
			length := uint32(le.Uint16(e[4:]))
			start := uint64(le.Uint16(e[6:]))<<32 | uint64(le.Uint32(e[8:]))
			initialized := length <= 32768 // longer means "unwritten"
			if !initialized {
				length -= 32768
			}
			if err := fn(lblock, start, length, initialized); err != nil {
				return err
			}
			continue
		}
		child := int64(le.Uint16(e[8:]))<<32 | int64(le.Uint32(e[4:]))
		b, err := fs.blocks(child, 1)
		if err != nil {
			return err
		}
		if err := fs.extents(b, depth+1, fn); err != nil {
			return err
		}
	}
	return nil
}

// blockMap returns the physical block of each of the first count logical
// blocks (0 = hole), ext2/3 style.
func (fs *Ext4) blockMap(iblock []byte, count int64) ([]uint32, error) {
	le := binary.LittleEndian
	per := fs.blockSize / 4
	ptr := func(i int) uint32 { return le.Uint32(iblock[4*i:]) }

	var walk func(block uint32, level int, limit int64) ([]uint32, error)
	walk = func(block uint32, level int, limit int64) ([]uint32, error) {
		if level == 0 {
			return []uint32{block}, nil
		}
		if block == 0 { // a hole covering everything below this pointer
			span := per
			for i := 1; i < level; i++ {
				span *= per
			}
			return make([]uint32, min(limit, span)), nil
		}
		b, err := fs.blocks(int64(block), 1)
		if err != nil {
			return nil, err
		}
		var out []uint32
		for i := int64(0); i < per && int64(len(out)) < limit; i++ {
			sub, err := walk(le.Uint32(b[4*i:]), level-1, limit-int64(len(out)))
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		}
		return out, nil
	}

	out := make([]uint32, 0, count)
	for i := 0; i < 12; i++ {
		out = append(out, ptr(i))
	}
	for level := 1; level <= 3 && int64(len(out)) < count; level++ {
		sub, err := walk(ptr(11+level), level, count-int64(len(out)))
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	if int64(len(out)) > count {
		out = out[:count]
	}
	return out, nil
}

// ReadFile returns the contents of a regular file.
func (fs *Ext4) ReadFile(ino Inode) ([]byte, error) {
	if ino.Size > maxFileSize {
		return nil, errorf("file too large (%d bytes): corrupt image?", ino.Size)
	}
	size := int64(ino.Size)
	if ino.Flags&inlineDataFl != 0 {
		if size > 60 {
			return nil, errorf("inline data larger than 60 bytes is not supported")
		}
		return append([]byte(nil), ino.block[:size]...), nil
	}
	bs := fs.blockSize
	count := (size + bs - 1) / bs
	out := make([]byte, count*bs) // holes stay zero

	if ino.Flags&extentsFl != 0 {
		err := fs.extents(ino.block[:], 0, func(l uint32, p uint64, n uint32, init bool) error {
			lb := int64(l)
			if !init || lb >= count {
				return nil
			}
			length := min(int64(n), count-lb)
			data, err := fs.blocks(int64(p), length)
			if err != nil {
				return err
			}
			copy(out[lb*bs:], data)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		pblocks, err := fs.blockMap(ino.block[:], count)
		if err != nil {
			return nil, err
		}
		for l, p := range pblocks {
			if p == 0 {
				continue
			}
			data, err := fs.blocks(int64(p), 1)
			if err != nil {
				return nil, err
			}
			copy(out[int64(l)*bs:], data)
		}
	}
	return out[:size], nil
}

// ListDir returns the entries of a directory, name -> inode number.
func (fs *Ext4) ListDir(n uint32) (map[string]uint32, error) {
	ino, err := fs.Inode(n)
	if err != nil {
		return nil, err
	}
	if !ino.IsDir() {
		return nil, errorf("not a directory")
	}
	if ino.Flags&inlineDataFl != 0 {
		return nil, errorf("inline directories are not supported")
	}
	data, err := fs.ReadFile(ino)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	entries := map[string]uint32{}
	// Linear scan: also correct for hashed (dir_index) directories, whose
	// index blocks look like empty entries to old readers by design
	for pos := 0; pos+8 <= len(data); {
		num := le.Uint32(data[pos:])
		recLen := int(le.Uint16(data[pos+4:]))
		nameLen := int(data[pos+6])
		if recLen < 8 || pos+8+nameLen > len(data) {
			return nil, errorf("corrupt directory entry")
		}
		if num != 0 && nameLen > 0 {
			name := strings.ToValidUTF8(string(data[pos+8:pos+8+nameLen]), "�")
			if name != "." && name != ".." {
				entries[name] = num
			}
		}
		pos += recLen
	}
	return entries, nil
}

// Lookup resolves a slash-separated path from the root. ok is false when
// some part of it doesn't exist.
func (fs *Ext4) Lookup(path string) (n uint32, ok bool, err error) {
	n = rootInode
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		ino, err := fs.Inode(n)
		if err != nil {
			return 0, false, err
		}
		if !ino.IsDir() {
			return 0, false, nil
		}
		entries, err := fs.ListDir(n)
		if err != nil {
			return 0, false, err
		}
		if n, ok = entries[part]; !ok {
			return 0, false, nil
		}
	}
	return n, true, nil
}
