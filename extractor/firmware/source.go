// Package firmware reads the stock sounds out of an official Samsung firmware
// package without unpacking it: zip download, Odin .tar.md5, lz4, Android
// sparse image and ext2/3/4 are all read in place.
package firmware

import (
	"errors"
	"fmt"
	"io"
)

// Error is a problem with the input that the user can act on, as opposed to
// an I/O failure or a bug. Unsupported marks real firmware this tool can't
// read, as opposed to a file that isn't firmware at all.
type Error struct {
	Msg         string
	Unsupported bool
}

func (e *Error) Error() string { return e.Msg }

func errorf(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

func unsupportedf(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...), Unsupported: true}
}

// The sounds of a new phone are what the user is trying to replace, so this
// is almost always the wrong download rather than a missing feature.
func errNewFirmware(what string) error {
	return unsupportedf("%s is the firmware of a phone with Android 10 or newer, "+
		"which can't be read (its system is inside super.img / EROFS). "+
		"Download the firmware of the old Galaxy you want the sounds from, "+
		"e.g. GT-I9300 for the Galaxy S3.", what)
}

// Source gives random access to an image: a whole file, a member inside an
// uncompressed tar or zip, or a decoded sparse image.
type Source interface {
	io.ReaderAt
	Size() int64
}

// readInto fills buf from src at off. Reads past the end mean the image is
// truncated or a structure in it points to garbage.
func readInto(src Source, buf []byte, off int64) error {
	if off < 0 || off+int64(len(buf)) > src.Size() {
		return errorf("image is truncated or corrupt (read past the end)")
	}
	n, err := src.ReadAt(buf, off)
	if n < len(buf) {
		if err == nil || errors.Is(err, io.EOF) {
			return errorf("image is truncated (unexpected end of file)")
		}
		return err
	}
	return nil
}

// readExact reads exactly n bytes at off.
func readExact(src Source, off int64, n int) ([]byte, error) {
	if off < 0 || n < 0 || off+int64(n) > src.Size() {
		return nil, errorf("image is truncated or corrupt (read past the end)")
	}
	buf := make([]byte, n)
	if err := readInto(src, buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}
