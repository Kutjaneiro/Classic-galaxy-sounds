package firmware

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// A small fake Galaxy S3 system partition
var s3Files = map[string][]byte{
	"build.prop":                                 []byte("# test\nro.product.model=GT-I9300\nro.build.version.release=4.3\nro.build.PDA=I9300XXUGNG3\n"),
	"media/audio/ui/Lock.ogg":                    noise(3000, 1),
	"media/audio/ui/Unlock.ogg":                  noise(5000, 2),
	"media/audio/ui/readme.txt":                  []byte("not audio"),
	"media/audio/ringtones/Over_the_horizon.ogg": noise(40_000, 3),
	"media/audio/notifications/Whistle.ogg":      noise(9000, 4),
	"media/audio/alarms/Morning_flower.ogg":      noise(20_000, 5),
	"app/Phone.apk":                              noise(4000, 6),
}

type mem []byte

func (m mem) Size() int64 { return int64(len(m)) }
func (m mem) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(m)) {
		return 0, errors.New("read out of range")
	}
	return copy(p, m[off:]), nil
}

func mustExt4(t *testing.T, src Source) *Ext4 {
	t.Helper()
	fs, err := OpenExt4(src)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func readPath(t *testing.T, fs *Ext4, p string) []byte {
	t.Helper()
	n, ok, err := fs.Lookup(p)
	if err != nil || !ok {
		t.Fatalf("lookup %s: ok=%v err=%v", p, ok, err)
	}
	ino, err := fs.Inode(n)
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(ino)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return data
}

func checkAll(t *testing.T, fs *Ext4, files map[string][]byte) {
	t.Helper()
	for p, want := range files {
		if got := readPath(t, fs, p); !bytes.Equal(got, want) {
			t.Errorf("%s: got %d bytes, want %d (content differs)", p, len(got), len(want))
		}
	}
}

// --- ext2/3/4 reader --------------------------------------------------------

func TestExt4Reader(t *testing.T) {
	holeData := append(append(noise(1024, 9), make([]byte, 14*1024)...), noise(1024, 10)...)
	extentHoles := append(append(append(make([]byte, 4096), noise(4096, 13)...), make([]byte, 8192)...), noise(10, 14)...)
	cases := []struct {
		name  string
		files map[string][]byte
		opts  buildOpts
	}{
		{"block map direct, indirect, double indirect",
			map[string][]byte{"small": []byte("hello"), "indirect": noise(20*1024, 7), "double": noise(300*1024, 8)},
			buildOpts{blockSize: 1024}},
		{"block map holes spanning the indirect block",
			map[string][]byte{"sparse": holeData}, buildOpts{blockSize: 1024, holes: true}},
		{"extents in the inode",
			map[string][]byte{"f": noise(7*4096+100, 11)}, buildOpts{blockSize: 4096, extents: true, fragment: true}},
		{"extent tree depth 1",
			map[string][]byte{"f": noise(40*1024, 12)}, buildOpts{blockSize: 1024, extents: true, fragment: true, extentDepth: 1}},
		{"extents with holes",
			map[string][]byte{"f": extentHoles}, buildOpts{blockSize: 4096, extents: true, holes: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkAll(t, mustExt4(t, mem(buildExt(t, c.files, c.opts))), c.files)
		})
	}
}

func TestLargeInodesAndManyDirEntries(t *testing.T) {
	files := map[string][]byte{}
	for i := 0; i < 120; i++ {
		files[fmt.Sprintf("media/audio/ui/sound_with_a_long_name_%03d.ogg", i)] = noise(100, int64(i))
	}
	fs := mustExt4(t, mem(buildExt(t, files, buildOpts{blockSize: 1024, inodeSize: 256})))
	checkAll(t, fs, files)
	n, _, _ := fs.Lookup("media/audio/ui")
	entries, err := fs.ListDir(n)
	if err != nil || len(entries) != 120 {
		t.Fatalf("got %d entries, err %v", len(entries), err)
	}
}

func TestLookup(t *testing.T) {
	fs := mustExt4(t, mem(buildExt(t, s3Files, buildOpts{})))
	if _, ok, err := fs.Lookup("media/nope"); ok || err != nil {
		t.Error("missing path found")
	}
	if _, ok, err := fs.Lookup("build.prop/child"); ok || err != nil {
		t.Error("path through a file found")
	}
	if ok, err := isDir(fs, "media/audio"); !ok || err != nil {
		t.Error("media/audio is not a directory")
	}
}

func TestNotExt(t *testing.T) {
	if _, err := OpenExt4(mem(make([]byte, 8192))); err == nil {
		t.Fatal("zeros accepted as ext4")
	}
}

// --- sparse -----------------------------------------------------------------

func TestSparseAllChunkTypes(t *testing.T) {
	raw := bytes.Join([][]byte{noise(4096*3, 20), make([]byte, 4096*5),
		bytes.Repeat([]byte{0xab, 0xcd, 0xef, 0x01}, 1024*2), noise(4096, 21)}, nil)
	s, err := OpenSparse(mem(toSparse(raw, 4096, 0)))
	if err != nil {
		t.Fatal(err)
	}
	// whole image, then unaligned reads crossing chunk boundaries
	for _, r := range [][2]int{{0, len(raw)}, {4095, 2}, {4096*3 - 7, 30}, {4096*8 + 1, 4096 * 2}, {10, 4096*11 - 20}} {
		got := make([]byte, r[1])
		if _, err := s.ReadAt(got, int64(r[0])); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, raw[r[0]:r[0]+r[1]]) {
			t.Errorf("read %v differs", r)
		}
	}
}

func TestSparseSamsungExtendedHeaders(t *testing.T) {
	raw := append(noise(4096*2, 22), make([]byte, 4096)...)
	s, err := OpenSparse(mem(toSparse(raw, 4096, 4)))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(raw))
	if _, err := s.ReadAt(got, 0); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("extended headers not handled", err)
	}
}

func TestExt4InsideSparse(t *testing.T) {
	s, err := OpenSparse(mem(toSparse(buildExt(t, s3Files, buildOpts{blockSize: 4096}), 4096, 0)))
	if err != nil {
		t.Fatal(err)
	}
	checkAll(t, mustExt4(t, s), s3Files)
}

// --- whole firmware packages ------------------------------------------------

type fixture struct {
	t         *testing.T
	dir, out  string
	systemImg []byte
	log       []string
}

func newFixture(t *testing.T) *fixture {
	dir := t.TempDir()
	return &fixture{t: t, dir: dir, out: filepath.Join(dir, "out"),
		systemImg: toSparse(buildExt(t, s3Files, buildOpts{blockSize: 4096}), 4096, 0)}
}

func (f *fixture) logf(format string, a ...any) { f.log = append(f.log, fmt.Sprintf(format, a...)) }

func (f *fixture) logged(s string) bool {
	for _, l := range f.log {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

type member struct {
	name string
	data []byte
}

// odin writes an Odin package: a ustar tar plus "<md5>  <name>.tar\n".
func (f *fixture) odin(name string, members []member, corrupt bool) string {
	if members == nil {
		members = []member{{"boot.img", noise(2000, 30)}, {"system.img", f.systemImg}, {"modem.bin", []byte("x")}}
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		tw.WriteHeader(&tar.Header{Name: m.name, Size: int64(len(m.data)), Mode: 0o644, Format: tar.FormatUSTAR})
		tw.Write(m.data)
	}
	tw.Close()
	data := buf.Bytes()
	sum := md5.Sum(data)
	if corrupt {
		data[len(data)-2000] ^= 0xFF
	}
	p := filepath.Join(f.dir, name)
	footer := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), strings.TrimSuffix(name, ".md5"))
	if err := os.WriteFile(p, append(data, footer...), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) zip(name string, method uint16, files map[string]string) string {
	p := filepath.Join(f.dir, name)
	out, err := os.Create(p)
	if err != nil {
		f.t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for inner, src := range files {
		data := []byte(src)
		if b, err := os.ReadFile(src); err == nil {
			data = b
		}
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: inner, Method: method})
		w.Write(data)
	}
	zw.Close()
	out.Close()
	return p
}

// extract runs the whole pipeline like the command does.
func (f *fixture) extract(firmwarePath string, verify bool) (int, error) {
	img, err := Open(firmwarePath, Options{Verify: verify, TempDir: f.dir, Log: f.logf})
	if err != nil {
		return 0, err
	}
	defer img.Close()
	fs, err := OpenExt4(img)
	if err != nil {
		return 0, err
	}
	return Extract(fs, f.out, "../../maps", f.logf)
}

func (f *fixture) assertExtracted() {
	f.t.Helper()
	for _, s := range [][2]string{{"ui", "Lock.ogg"}, {"ui", "Unlock.ogg"}, {"ringtones", "Over_the_horizon.ogg"},
		{"notifications", "Whistle.ogg"}, {"alarms", "Morning_flower.ogg"}} {
		got, err := os.ReadFile(filepath.Join(f.out, s[0], s[1]))
		if err != nil || !bytes.Equal(got, s3Files["media/audio/"+s[0]+"/"+s[1]]) {
			f.t.Errorf("%s/%s not extracted correctly (%v)", s[0], s[1], err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.out, "ui", "readme.txt")); err == nil {
		f.t.Error("non-audio file extracted")
	}
	if info, _ := os.ReadFile(filepath.Join(f.out, "firmware-info.txt")); !bytes.Contains(info, []byte("GT-I9300")) {
		f.t.Error("firmware-info.txt missing the model")
	}
	if _, err := os.Stat(filepath.Join(f.out, ".nomedia")); err != nil {
		f.t.Error("folder must be hidden from media apps (.nomedia)")
	}
}

func wantUserError(t *testing.T, err error) {
	t.Helper()
	var ferr *Error
	if !errors.As(err, &ferr) {
		t.Fatalf("want a firmware.Error, got %v", err)
	}
}

func TestTarMD5(t *testing.T) {
	f := newFixture(t)
	if _, err := f.extract(f.odin("I9300XXUGNG3_HOME.tar.md5", nil, false), true); err != nil {
		t.Fatal(err)
	}
	f.assertExtracted()
	if !f.logged("MD5 OK") {
		t.Error("MD5 not verified")
	}
	if _, err := os.Stat(filepath.Join(f.out, "ui-map.conf")); err != nil {
		t.Error("the S3 map should be picked by model")
	}
}

func TestCorruptDownloadIsRejected(t *testing.T) {
	f := newFixture(t)
	_, err := f.extract(f.odin("I9300XXUGNG3_HOME.tar.md5", nil, true), true)
	wantUserError(t, err)
	if _, err := os.Stat(f.out); err == nil {
		t.Error("output written for a corrupt download")
	}
}

func TestZipDownloads(t *testing.T) {
	for _, method := range []uint16{zip.Deflate, zip.Store} {
		t.Run(fmt.Sprint("method ", method), func(t *testing.T) {
			f := newFixture(t)
			odin := f.odin("I9300XXUGNG3_HOME.tar.md5", nil, false)
			z := f.zip("firmware.zip", method, map[string]string{"I9300XXUGNG3_HOME.tar.md5": odin, "SS_DL.dll": "tool"})
			if _, err := f.extract(z, true); err != nil {
				t.Fatal(err)
			}
			f.assertExtracted()
			if !f.logged("MD5 OK") {
				t.Error("MD5 not verified inside the zip")
			}
		})
	}
}

func TestCorruptTarInsideZipIsRejected(t *testing.T) {
	f := newFixture(t)
	odin := f.odin("AP_X.tar.md5", nil, true)
	_, err := f.extract(f.zip("firmware.zip", zip.Deflate, map[string]string{"AP_X.tar.md5": odin}), true)
	wantUserError(t, err)
}

func TestTarThatLooksLikeAZip(t *testing.T) {
	// Real S3 firmware: zip-like bytes (APKs) near the end of the tar
	f := newFixture(t)
	cache := append(append([]byte("PK\x05\x06"), make([]byte, 18)...), noise(100, 31)...)
	odin := f.odin("AP_X.tar.md5", []member{{"system.img", f.systemImg}, {"cache.img", cache}}, false)
	if _, err := f.extract(f.zip("firmware.zip", zip.Deflate, map[string]string{"AP_X.tar.md5": odin}), true); err != nil {
		t.Fatal(err)
	}
	f.assertExtracted()
}

func TestSplitFirmwareWithoutSystemImage(t *testing.T) {
	f := newFixture(t)
	_, err := f.extract(f.odin("BL_X.tar.md5", []member{{"sboot.bin", []byte("x")}}, false), true)
	wantUserError(t, err)
	if !strings.Contains(err.Error(), "AP_") {
		t.Error("error should point to the AP_ file:", err)
	}
}

func TestSystemAsRootRawImage(t *testing.T) {
	f := newFixture(t)
	files := map[string][]byte{}
	for k, v := range s3Files {
		files["system/"+k] = v
	}
	img := filepath.Join(f.dir, "system.img")
	os.WriteFile(img, buildExt(t, files, buildOpts{blockSize: 4096, extents: true}), 0o644)
	if _, err := f.extract(img, true); err != nil {
		t.Fatal(err)
	}
	f.assertExtracted()
}

func TestLz4ImageInTar(t *testing.T) {
	f := newFixture(t)
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	w.Write(f.systemImg)
	w.Close()
	odin := f.odin("AP_X.tar.md5", []member{{"system.img.ext4.lz4", buf.Bytes()}}, false)
	if _, err := f.extract(odin, true); err != nil {
		t.Fatal(err)
	}
	f.assertExtracted()
}

func TestRefusesToMixWithExistingSounds(t *testing.T) {
	f := newFixture(t)
	os.MkdirAll(filepath.Join(f.out, "ui"), 0o755)
	os.WriteFile(filepath.Join(f.out, "ui", "old.ogg"), []byte("x"), 0o644)
	_, err := f.extract(f.odin("I9300XXUGNG3_HOME.tar.md5", nil, false), true)
	wantUserError(t, err)
	if _, err := os.Stat(filepath.Join(f.out, "ringtones")); err == nil {
		t.Error("extracted into a folder that already had sounds")
	}
}

func TestTempFilesAreRemoved(t *testing.T) {
	f := newFixture(t)
	odin := f.odin("AP_X.tar.md5", nil, false)
	z := f.zip("firmware.zip", zip.Deflate, map[string]string{"AP_X.tar.md5": odin})
	if _, err := f.extract(z, true); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(f.dir, "cgs-*.img")); len(left) > 0 {
		t.Error("temporary image left behind:", left)
	}
}

func TestFindMap(t *testing.T) {
	if m := FindMap("GT-I9300", "../../maps"); filepath.Base(m) != "galaxy-s3.conf" {
		t.Errorf("GT-I9300 -> %q", m)
	}
	if m := FindMap("SM-G900F", "../../maps"); m != "" {
		t.Errorf("S5 should have no map yet, got %q", m)
	}
}

// --- real mke2fs images ------------------------------------------------------

func findMke2fs() string {
	for _, c := range []string{os.Getenv("MKE2FS"), "mke2fs", "mkfs.ext4"} {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	if sdk := os.Getenv("LOCALAPPDATA"); sdk != "" {
		p := filepath.Join(sdk, "Android", "Sdk", "platform-tools", "mke2fs.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// The reader against images made by the real e2fsprogs, across features
func TestRealMke2fs(t *testing.T) {
	mkfs := findMke2fs()
	if mkfs == "" {
		t.Skip("mke2fs not found")
	}
	root := t.TempDir()
	files := map[string][]byte{"media/audio/ringtones/big.ogg": noise(3<<20, 40)}
	for k, v := range s3Files {
		files[k] = v
	}
	for p, data := range files {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), data, 0o644)
	}
	for _, c := range []struct {
		fstype string
		extra  []string
	}{
		{"ext2", nil}, {"ext3", nil}, {"ext4", nil},
		{"ext4", []string{"-O", "64bit,metadata_csum"}},
		{"ext4", []string{"-b", "1024", "-O", "^flex_bg"}},
	} {
		t.Run(c.fstype+" "+strings.Join(c.extra, " "), func(t *testing.T) {
			img := filepath.Join(t.TempDir(), "fs.img")
			args := append([]string{"-q", "-F", "-t", c.fstype, "-I", "256"}, c.extra...)
			cmd := exec.Command(mkfs, append(args, "-d", root, img, "16M")...)
			if conf := filepath.Join(filepath.Dir(mkfs), "mke2fs.conf"); fileExists(conf) {
				cmd.Env = append(os.Environ(), "MKE2FS_CONFIG="+conf)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				if strings.Contains(string(out), "populat") {
					t.Skip("this mke2fs cannot populate images (-d)")
				}
				t.Fatalf("mke2fs: %v\n%s", err, out)
			}
			data, err := os.ReadFile(img)
			if err != nil {
				t.Fatal(err)
			}
			fs := mustExt4(t, mem(data))
			checkAll(t, fs, files)
			if _, ok, _ := fs.Lookup("lost+found"); !ok {
				t.Error("lost+found missing")
			}
		})
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestIdentify(t *testing.T) {
	f := newFixture(t)
	odin := f.odin("AP_X.tar.md5", nil, false)
	good := map[string]string{
		"odin":   odin,
		"zip":    f.zip("fw.zip", zip.Deflate, map[string]string{"AP_X.tar.md5": odin}),
		"sparse": filepath.Join(f.dir, "system.img"),
	}
	os.WriteFile(good["sparse"], f.systemImg, 0o644)
	for kind, p := range good {
		if desc, err := Identify(p); err != nil || desc == "" {
			t.Errorf("%s not identified: %q %v", kind, desc, err)
		}
	}
	// Big zips of photos or music sit in Download too
	photos := f.zip("photos.zip", zip.Deflate, map[string]string{"img001.jpg": "jpeg", "notes.txt": "x"})
	text := filepath.Join(f.dir, "notes.img")
	os.WriteFile(text, bytes.Repeat([]byte("hello "), 1000), 0o644)
	for _, p := range []string{photos, text} {
		_, err := Identify(p)
		wantUserError(t, err)
	}
}

func wantUnsupported(t *testing.T, err error) {
	t.Helper()
	var ferr *Error
	if !errors.As(err, &ferr) || !ferr.Unsupported || !strings.Contains(ferr.Msg, "Android 10") {
		t.Fatalf("want an Android 10+ error, got %v", err)
	}
}

func TestAndroid10FirmwareIsExplained(t *testing.T) {
	f := newFixture(t)
	members := []member{{"boot.img.lz4", noise(100, 50)}, {"super.img.lz4", noise(5000, 51)}, {"vbmeta.img", noise(10, 52)}}
	odin := f.odin("AP_G980FXXU1ATB3.tar.md5", members, false)
	_, err := f.extract(odin, true)
	wantUnsupported(t, err)
	_, err = Identify(odin)
	wantUnsupported(t, err)
	// inside a zip download the tar is streamed, and stops at super.img
	_, err = f.extract(f.zip("s20.zip", zip.Deflate, map[string]string{"AP_X.tar.md5": odin}), true)
	wantUnsupported(t, err)
	_, err = Identify(f.zip("s20-stored.zip", zip.Store, map[string]string{"AP_X.tar.md5": odin}))
	wantUnsupported(t, err)
}

func TestEROFSIsExplained(t *testing.T) {
	img := make([]byte, 8192)
	le.PutUint32(img[1024:], erofsMagic)
	_, err := OpenExt4(mem(img))
	wantUnsupported(t, err)
}

func TestIdentifySkipsSplitFirmwareParts(t *testing.T) {
	f := newFixture(t)
	for _, part := range []string{"BL_X.tar.md5", "CP_X.tar.md5", "CSC_X.tar.md5"} {
		_, err := Identify(f.odin(part, []member{{"modem.bin", noise(100, 53)}}, false))
		var ferr *Error
		if !errors.As(err, &ferr) || ferr.Unsupported {
			t.Errorf("%s: want a plain 'not firmware' error, got %v", part, err)
		}
	}
	if desc, err := Identify(f.odin("AP_X.tar.md5", nil, false)); err != nil || !strings.Contains(desc, "system.img") {
		t.Errorf("AP part: %q %v", desc, err)
	}
}
