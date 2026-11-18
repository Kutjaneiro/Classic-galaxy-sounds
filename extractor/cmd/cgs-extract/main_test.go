package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The firmware package tests cover real extraction; these cover the
// command's argument handling and exit codes.

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	var opened []string
	code := run(args, &out, &errOut, func(p string) { opened = append(opened, p) })
	return code, out.String(), errOut.String()
}

func TestUsageWithoutArguments(t *testing.T) {
	code, _, stderr := runCmd(t)
	if code != 2 || !strings.Contains(stderr, "Usage") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
}

func TestMissingFile(t *testing.T) {
	code, _, stderr := runCmd(t, filepath.Join(t.TempDir(), "nope.zip"))
	if code != 1 || !strings.Contains(stderr, "file not found") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
}

func TestFlagsAfterTheFirmware(t *testing.T) {
	dir := t.TempDir()
	fw := filepath.Join(dir, "BL_X.tar")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "sboot.bin", Size: 1, Mode: 0o644, Format: tar.FormatUSTAR})
	tw.Write([]byte("x"))
	tw.Close()
	os.WriteFile(fw, buf.Bytes(), 0o644)

	code, stdout, stderr := runCmd(t, fw, "-o", filepath.Join(dir, "out"), "--no-verify")
	if code != 1 || !strings.Contains(stderr, "no system image") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestCheckRejectsOtherZips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "photos.zip")
	// an empty zip: just the end-of-central-directory record
	os.WriteFile(p, append([]byte("PK\x05\x06"), make([]byte, 18)...), 0o644)
	code, _, stderr := runCmd(t, "--check", p)
	if code != 1 || !strings.Contains(stderr, "no Odin package") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
}

func TestAndroid10FirmwareExitCode(t *testing.T) {
	dir := t.TempDir()
	fw := filepath.Join(dir, "AP_S24.tar")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "super.img.lz4", Size: 1, Mode: 0o644, Format: tar.FormatUSTAR})
	tw.Write([]byte("x"))
	tw.Close()
	os.WriteFile(fw, buf.Bytes(), 0o644)
	for _, args := range [][]string{{"--check", fw}, {fw, "-o", filepath.Join(dir, "out")}} {
		if code, _, stderr := runCmd(t, args...); code != 3 || !strings.Contains(stderr, "Android 10") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr)
		}
	}
}
