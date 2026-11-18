// Command cgs-extract extracts the stock sounds from an official Samsung
// firmware package, ready for the Classic Galaxy Sounds module.
//
//	cgs-extract FIRMWARE [-o OUTPUT] [--list] [--no-verify] [--maps DIR]
//
// The same program runs on a PC (Windows, macOS, Linux) and on the phone,
// where the module installer calls it.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Kutjaneiro/classic-galaxy-sounds/extractor/firmware"
)

func main() {
	standalone := launchedWithoutTerminal()
	status := run(os.Args[1:], os.Stdout, os.Stderr, func(out string) {
		if standalone {
			openFolder(out) // show the result to drag-and-drop users
		}
	})
	if standalone {
		fmt.Print("\nPress Enter to close...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(status)
}

func run(args []string, stdout, stderr io.Writer, onSuccess func(string)) int {
	fl := flag.NewFlagSet("cgs-extract", flag.ContinueOnError)
	fl.SetOutput(stderr)
	output := fl.String("o", "", "output folder (default: ClassicGalaxySounds-<build> next to the firmware)")
	list := fl.Bool("list", false, "only list the sounds")
	check := fl.Bool("check", false, "only tell whether the file is a firmware package (fast, reads no image)")
	noVerify := fl.Bool("no-verify", false, "skip the .tar.md5 integrity check")
	maps := fl.String("maps", defaultMapsDir(), "folder with the UI maps (maps/*.conf)")
	tmp := fl.String("tmp", "", "folder for temporary files (default: system temp)")
	installer := fl.Bool("installer", false, "called by the module installer: no PC instructions")
	fl.Usage = func() {
		fmt.Fprintln(stderr, "Extract the stock sounds from an official Samsung firmware package.")
		fmt.Fprintln(stderr, "\nUsage: cgs-extract FIRMWARE [options]   (.zip, .tar.md5, .tar, .img or .lz4)")
		fl.PrintDefaults()
	}

	// Flags may come before or after the firmware path
	var positional []string
	for {
		if err := fl.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if fl.NArg() == 0 {
			break
		}
		positional = append(positional, fl.Arg(0))
		args = fl.Args()[1:]
	}
	if len(positional) != 1 {
		fl.Usage()
		return 2
	}
	fw := positional[0]

	log := func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }
	fail := func(err error) int {
		var ferr *firmware.Error
		if errors.As(err, &ferr) {
			fmt.Fprintf(stderr, "\nerror: %s\n", ferr.Msg)
			if ferr.Unsupported {
				return 3 // firmware, but not usable: the installer shows why
			}
			return 1
		}
		fmt.Fprintf(stderr, "\nunexpected error: %v\nPlease report it with the text above.\n", err)
		return 2
	}

	if *check {
		desc, err := firmware.Identify(fw)
		if err != nil {
			return fail(err)
		}
		log("%s", desc)
		return 0
	}

	img, err := firmware.Open(fw, firmware.Options{Verify: !*noVerify, TempDir: *tmp, Log: log})
	if err != nil {
		return fail(err)
	}
	defer img.Close()
	fs, err := firmware.OpenExt4(img)
	if err != nil {
		return fail(err)
	}
	if *list {
		if _, err := firmware.List(fs, log); err != nil {
			return fail(err)
		}
		return 0
	}

	out := *output
	if out == "" {
		out = firmware.DefaultOutput(fw, firmware.ReadInfo(fs))
	}
	if abs, err := filepath.Abs(out); err == nil {
		out = abs
	}
	total, err := firmware.Extract(fs, out, *maps, log)
	if err != nil {
		return fail(err)
	}
	if total == 0 {
		return fail(&firmware.Error{Msg: "the image has a media/audio folder but no sounds in it"})
	}
	if *installer {
		log("Done: %d sounds", total)
	} else {
		log("\nDone: %d sounds saved to\n  %s", total, out)
		log("\nNext: copy that folder to the One UI phone as /sdcard/ClassicGalaxySounds")
		log("  adb push \"%s\" /sdcard/ClassicGalaxySounds", out)
		log("then flash the module and reboot.")
		log("Or skip the PC: put the firmware file in the phone's Download folder")
		log("and the module extracts the sounds itself while installing.")
	}
	if onSuccess != nil {
		onSuccess(out)
	}
	return 0
}

// defaultMapsDir finds maps/ next to the program or one level up (the repo
// layout when running a build from bin/ or extractor/).
func defaultMapsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, candidate := range []string{filepath.Join(dir, "maps"), filepath.Join(dir, "..", "maps")} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	return ""
}
