package firmware

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pierrec/lz4/v4"
)

// Logf prints a progress line.
type Logf func(format string, args ...any)

// Options control Open.
type Options struct {
	Verify  bool   // check the MD5 footer of Odin .tar.md5 packages
	TempDir string // where to put a decompressed system image ("" = system temp)
	Log     Logf
}

// Image is an opened system image. Close releases the files and deletes
// any temporary copy.
type Image struct {
	Source
	closers []func() error
}

// Close releases everything Open acquired.
func (img *Image) Close() error {
	var first error
	for i := len(img.closers) - 1; i >= 0; i-- {
		if err := img.closers[i](); err != nil && first == nil {
			first = err
		}
	}
	img.closers = nil
	return first
}

var (
	// Old Galaxies call the system partition factoryfs (S2) or system (S3 and later)
	systemImage = regexp.MustCompile(`(?i)(^|/)(system|factoryfs)\.img(\.ext4)?(\.lz4)?$`)
	tarName     = regexp.MustCompile(`(?i)\.tar(\.md5)?$`)
	// Android 10+ firmware: dynamic partitions instead of system.img
	superImage = regexp.MustCompile(`(?i)(^|/)super\.img(\.ext4)?(\.lz4)?$`)
	md5Footer  = regexp.MustCompile(`([0-9a-fA-F]{32})\s+\S+\s*$`)
	lz4Magic   = []byte{0x04, 0x22, 0x4d, 0x18}
)

// Open returns the system image inside a firmware package: a zip download,
// an Odin .tar / .tar.md5, an lz4 or sparse image, or a raw ext4 image.
func Open(file string, opt Options) (*Image, error) {
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	o := &opener{img: &Image{}, opt: opt}
	if err := o.open(file); err != nil {
		o.img.Close()
		return nil, err
	}
	return o.img, nil
}

type opener struct {
	img *Image
	opt Options
}

func (o *opener) onClose(fn func() error) { o.img.closers = append(o.img.closers, fn) }

func (o *opener) open(file string) error {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return errorf("file not found: %s", file)
	} else if err != nil {
		return err
	}
	o.onClose(f.Close)
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return errorf("%s is a folder, not a firmware file", file)
	}
	name := filepath.Base(file)
	src := io.NewSectionReader(f, 0, st.Size())
	head := make([]byte, 512)
	n, _ := f.ReadAt(head, 0)
	head = head[:n]

	// Detect by the header, not by looking for a zip directory at the end:
	// Odin tars full of APKs have zip signatures near their end too
	if !looksLikeTar(name, head) && (bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06"))) {
		return o.openZip(f, st.Size(), name)
	}
	return o.openMember(src, name)
}

// Identify cheaply tells whether file looks like firmware Open can read,
// without decompressing anything: it reads a zip's directory or the first
// bytes of anything else. It returns a short description.
func Identify(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	name := filepath.Base(file)
	head := make([]byte, 512)
	n, _ := f.ReadAt(head, 0)
	head = head[:n]
	switch {
	case looksLikeTar(name, head):
		// The headers are enough: skips the BL / CP / CSC parts of a split
		// firmware and recognises Android 10+ packages
		return identifyTar(io.NewSectionReader(f, 0, st.Size()), name)
	case bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06")):
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return "", errorf("%s is not a valid zip file", name)
		}
		m := pickZipMember(zr.File)
		if m == nil {
			return "", errorf("%s contains no Odin package or system image", name)
		}
		inner := path.Base(m.Name)
		if off, err := m.DataOffset(); err == nil && m.Method == zip.Store && tarName.MatchString(inner) {
			if _, err := identifyTar(io.NewSectionReader(f, off, int64(m.UncompressedSize64)), inner); err != nil {
				return "", err
			}
		}
		return "firmware zip with " + inner, nil
	case bytes.HasPrefix(head, lz4Magic):
		return "lz4 image", nil
	case len(head) >= 4 && binary.LittleEndian.Uint32(head) == sparseMagic:
		return "sparse image", nil
	}
	if sb, err := readExact(io.NewSectionReader(f, 0, st.Size()), 1024+56, 2); err == nil && binary.LittleEndian.Uint16(sb) == 0xEF53 {
		return "ext4 image", nil
	}
	return "", errorf("%s is not a firmware package", name)
}

func identifyTar(src Source, name string) (string, error) {
	hdr, _, names, err := scanTar(src, name)
	if err != nil {
		return "", err
	}
	if hdr == nil {
		return "", noSystemImage(name, names)
	}
	return "Odin package with " + path.Base(hdr.Name), nil
}

func looksLikeTar(name string, head []byte) bool {
	return tarName.MatchString(name) || len(head) >= 262 && string(head[257:262]) == "ustar"
}

// openMember opens something with random access: a tar or an image.
func (o *opener) openMember(src Source, name string) error {
	head, _ := readExact(src, 0, int(min(512, src.Size())))
	if looksLikeTar(name, head) {
		o.opt.Log("Odin package: %s", name)
		if o.opt.Verify && strings.HasSuffix(strings.ToLower(name), ".md5") {
			if err := o.verifyMD5(src, name); err != nil {
				return err
			}
		}
		return o.openTar(src, name)
	}
	o.opt.Log("Image: %s", name)
	return o.setImage(src, name)
}

// pickZipMember prefers a system image, then an AP_/PDA_/CODE_ Odin package,
// then any tar.
func pickZipMember(files []*zip.File) *zip.File {
	rank := func(f *zip.File) int {
		base := strings.ToUpper(path.Base(f.Name))
		r := 0
		if !systemImage.MatchString(f.Name) {
			r += 4
		}
		if !(strings.HasPrefix(base, "AP_") || strings.HasPrefix(base, "PDA_") || strings.HasPrefix(base, "CODE_")) {
			r += 2
		}
		if !tarName.MatchString(base) {
			r++
		}
		return r
	}
	var candidates []*zip.File
	for _, f := range files {
		if systemImage.MatchString(f.Name) || tarName.MatchString(f.Name) {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return rank(candidates[i]) < rank(candidates[j]) })
	return candidates[0]
}

func (o *opener) openZip(f *os.File, size int64, name string) error {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return errorf("%s is not a valid zip file: %v", name, err)
	}
	member := pickZipMember(zr.File)
	if member == nil {
		return errorf("%s contains no Odin package or system image", name)
	}
	inner := path.Base(member.Name)

	// Stored (uncompressed) member: read it in place, no copy at all
	if member.Method == zip.Store {
		off, err := member.DataOffset()
		if err != nil {
			return err
		}
		o.opt.Log("Reading %s inside %s", inner, name)
		return o.openMember(io.NewSectionReader(f, off, int64(member.UncompressedSize64)), inner)
	}

	o.opt.Log("Decompressing %s from %s...", inner, name)
	rc, err := member.Open()
	if err != nil {
		return errorf("%s is corrupt: %v", name, err)
	}
	defer rc.Close()
	r := &progressReader{r: rc, total: int64(member.UncompressedSize64), log: o.opt.Log}
	if tarName.MatchString(inner) {
		return o.streamTar(r, inner)
	}
	tmp, err := o.tempCopy(r, int64(member.UncompressedSize64))
	if err != nil {
		return err
	}
	return o.setImage(tmp, inner)
}

// scanTar finds the system image in a tar with random access, reading only
// the headers. hdr is nil when there is none; names lists the files seen.
func scanTar(src Source, name string) (hdr *tar.Header, offset int64, names []string, err error) {
	cr := &countingReader{r: io.NewSectionReader(src, 0, src.Size())}
	tr := tar.NewReader(cr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, 0, names, nil
		}
		if err != nil {
			return nil, 0, names, errorf("%s is not a valid tar file: %v", name, err)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		names = append(names, hdr.Name)
		if systemImage.MatchString(hdr.Name) {
			// After Next, the reader sits at the start of the member's data
			return hdr, cr.n, names, nil
		}
		if superImage.MatchString(hdr.Name) {
			return nil, 0, names, errNewFirmware(name)
		}
	}
}

// openTar opens the system image inside a tar in place.
func (o *opener) openTar(src Source, name string) error {
	hdr, off, names, err := scanTar(src, name)
	if err != nil {
		return err
	}
	if hdr == nil {
		return noSystemImage(name, names)
	}
	o.opt.Log("  found %s (%d MB)", hdr.Name, hdr.Size/1e6)
	return o.setImage(io.NewSectionReader(src, off, hdr.Size), hdr.Name)
}

// streamTar reads a tar that can only be read front to back (a compressed
// zip member): the system image is copied to a temp file, and the MD5 footer
// is checked on the same pass.
func (o *opener) streamTar(r io.Reader, name string) error {
	o.opt.Log("Odin package: %s", name)
	var sum *tailHasher
	if o.opt.Verify && strings.HasSuffix(strings.ToLower(name), ".md5") {
		sum = &tailHasher{h: md5.New()}
		r = io.TeeReader(r, sum)
	}
	tr := tar.NewReader(r)
	var names []string
	var image Source
	var imageName string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errorf("%s is not a valid tar file: %v", name, err)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		names = append(names, hdr.Name)
		if image == nil && superImage.MatchString(hdr.Name) {
			return errNewFirmware(name) // no need to read the rest
		}
		if image == nil && systemImage.MatchString(hdr.Name) {
			o.opt.Log("  found %s (%d MB)", hdr.Name, hdr.Size/1e6)
			if image, err = o.tempCopy(tr, hdr.Size); err != nil {
				return err
			}
			imageName = hdr.Name
			if sum == nil {
				break // nothing else to read
			}
		}
	}
	if image == nil {
		return noSystemImage(name, names)
	}
	if sum != nil {
		if _, err := io.Copy(io.Discard, r); err != nil { // the footer is after the tar
			return errorf("%s is corrupt: %v", name, err)
		}
		if err := sum.check(name, o.opt.Log); err != nil {
			return err
		}
	}
	return o.setImage(image, imageName)
}

func noSystemImage(name string, names []string) error {
	list := strings.Join(names, ", ")
	if list == "" {
		list = "nothing"
	}
	return errorf("no system image in %s (it contains: %s). "+
		"In split firmwares the system image is in the AP_ file.", name, list)
}

// setImage undoes lz4 compression and sparse encoding.
func (o *opener) setImage(src Source, name string) error {
	if head, err := readExact(src, 0, 4); err == nil && bytes.Equal(head, lz4Magic) {
		o.opt.Log("  decompressing %s...", path.Base(name))
		// lz4 typically halves a system image
		tmp, err := o.tempCopy(lz4.NewReader(io.NewSectionReader(src, 0, src.Size())), 2*src.Size())
		if err != nil {
			return errorf("%s: lz4 data is corrupt: %v", name, err)
		}
		src = tmp
	}
	if IsSparse(src) {
		o.opt.Log("  sparse image")
		sparse, err := OpenSparse(src)
		if err != nil {
			return err
		}
		src = sparse
	}
	o.img.Source = src
	return nil
}

// tempCopy writes r (about size bytes) to a temporary file, deleted again
// by Close.
func (o *opener) tempCopy(r io.Reader, size int64) (Source, error) {
	f, err := os.CreateTemp(o.opt.TempDir, "cgs-*.img")
	if err != nil {
		return nil, err
	}
	o.onClose(func() error { f.Close(); return os.Remove(f.Name()) })
	w := &fileWriter{f: f}
	n, err := io.CopyBuffer(w, r, make([]byte, 1<<20))
	if w.err != nil { // almost always a full disk
		need := ""
		if size > 0 {
			need = fmt.Sprintf(" It needs about %d MB of free space.", size>>20+1)
		}
		return nil, errorf("could not write the temporary system image to %s (%v).%s "+
			"Free up some space and try again.", filepath.Dir(f.Name()), w.err, need)
	}
	if err != nil {
		var ferr *Error
		if errors.As(err, &ferr) {
			return nil, err
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, zip.ErrChecksum) {
			return nil, errorf("the firmware is truncated or corrupt: %v", err)
		}
		return nil, err // e.g. disk full
	}
	return io.NewSectionReader(f, 0, n), nil
}

// verifyMD5 checks an Odin .tar.md5: a tar with "<md5>  <name>.tar\n" appended.
func (o *opener) verifyMD5(src Source, name string) error {
	tailLen := min(512, src.Size())
	tail, err := readExact(src, src.Size()-tailLen, int(tailLen))
	if err != nil {
		return err
	}
	m := md5Footer.FindSubmatchIndex(tail)
	if m == nil {
		o.opt.Log("  no MD5 footer, skipping integrity check")
		return nil
	}
	tarSize := src.Size() - tailLen + int64(m[0])
	o.opt.Log("  verifying MD5...")
	h := md5.New()
	r := &progressReader{r: io.NewSectionReader(src, 0, tarSize), total: tarSize, log: o.opt.Log}
	if _, err := io.CopyBuffer(h, r, make([]byte, 1<<20)); err != nil {
		return err
	}
	return compareMD5(h, string(tail[m[2]:m[3]]), name, o.opt.Log)
}

func compareMD5(h hash.Hash, want, name string, log Logf) error {
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return errorf("MD5 mismatch in %s: the download is corrupt", name)
	}
	log("  MD5 OK")
	return nil
}

// tailHasher hashes a stream while holding back its last 512 bytes, which
// may contain the MD5 footer (not part of what the footer's sum covers).
type tailHasher struct {
	h    hash.Hash
	tail []byte
}

func (t *tailHasher) Write(p []byte) (int, error) {
	t.tail = append(t.tail, p...)
	if extra := len(t.tail) - 512; extra > 0 {
		t.h.Write(t.tail[:extra])
		t.tail = append(t.tail[:0], t.tail[extra:]...)
	}
	return len(p), nil
}

func (t *tailHasher) check(name string, log Logf) error {
	m := md5Footer.FindSubmatchIndex(t.tail)
	if m == nil {
		log("  no MD5 footer, skipping integrity check")
		return nil
	}
	t.h.Write(t.tail[:m[0]])
	return compareMD5(t.h, string(t.tail[m[2]:m[3]]), name, log)
}

// fileWriter remembers write errors, to tell them apart from read errors.
type fileWriter struct {
	f   *os.File
	err error
}

func (w *fileWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

// countingReader tracks the position, so the data offset of a tar member is
// known after tar.Reader.Next. Seek lets tar skip members without reading.
type countingReader struct {
	r *io.SectionReader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n += int64(k)
	return k, err
}

func (c *countingReader) Seek(offset int64, whence int) (int64, error) {
	pos, err := c.r.Seek(offset, whence)
	if err == nil {
		c.n = pos
	}
	return pos, err
}

// progressReader logs every 10% of a long read.
type progressReader struct {
	r     io.Reader
	total int64
	done  int64
	last  int64
	log   Logf
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.total > 64<<20 { // only worth it for big files
		if pct := p.done * 100 / p.total; pct/10 > p.last/10 && pct < 100 {
			p.last = pct
			p.log("  %d%%", pct)
		}
	}
	return n, err
}
