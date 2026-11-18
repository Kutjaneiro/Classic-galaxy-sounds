package firmware

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Categories are the media/audio subfolders that get extracted.
var Categories = []string{"ui", "notifications", "ringtones", "alarms"}

var audioExtensions = []string{".ogg", ".mp3", ".wav", ".flac", ".m4a", ".aac"}

func isAudio(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range audioExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// Info identifies the firmware, from its build.prop.
type Info struct {
	Model, Android, Build string
}

func (i Info) String() string {
	model := i.Model
	if model == "" {
		model = "unknown"
	}
	return fmt.Sprintf("%s / Android %s / %s", model, i.Android, i.Build)
}

// Sound is an audio file in the image.
type Sound struct {
	Name  string
	Inode uint32
	Size  uint64
}

// FindAudioDir returns where media/audio lives: at the root of old system
// images, under system/ in system-as-root ones (Android 9+).
func FindAudioDir(fs *Ext4) (string, error) {
	for _, candidate := range []string{"media/audio", "system/media/audio"} {
		if ok, err := isDir(fs, candidate); err != nil || ok {
			return candidate, err
		}
	}
	return "", errorf("no media/audio folder in this system image")
}

func isDir(fs *Ext4, p string) (bool, error) {
	n, ok, err := fs.Lookup(p)
	if err != nil || !ok {
		return false, err
	}
	ino, err := fs.Inode(n)
	return err == nil && ino.IsDir(), err
}

// ReadInfo reads model, Android version and build from build.prop.
func ReadInfo(fs *Ext4) Info {
	props := map[string]string{}
	for _, candidate := range []string{"build.prop", "system/build.prop"} {
		n, ok, err := fs.Lookup(candidate)
		if err != nil || !ok {
			continue
		}
		ino, err := fs.Inode(n)
		if err != nil || !ino.IsFile() {
			continue
		}
		data, err := fs.ReadFile(ino)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			key, value, found := strings.Cut(sc.Text(), "=")
			if found && !strings.HasPrefix(strings.TrimSpace(key), "#") {
				props[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		break
	}
	info := Info{Model: props["ro.product.model"], Android: props["ro.build.version.release"], Build: props["ro.build.PDA"]}
	if info.Build == "" {
		info.Build = props["ro.build.display.id"]
	}
	if info.Android == "" {
		info.Android = "?"
	}
	if info.Build == "" {
		info.Build = "?"
	}
	return info
}

// AudioFiles lists the sounds of one category, sorted by name.
func AudioFiles(fs *Ext4, audioDir, category string) ([]Sound, error) {
	dir := audioDir + "/" + category
	if ok, err := isDir(fs, dir); err != nil || !ok {
		return nil, err
	}
	n, _, err := fs.Lookup(dir)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ListDir(n)
	if err != nil {
		return nil, err
	}
	var sounds []Sound
	for name, num := range entries {
		if !isAudio(name) {
			continue
		}
		ino, err := fs.Inode(num)
		if err != nil {
			return nil, err
		}
		if ino.IsFile() {
			sounds = append(sounds, Sound{Name: name, Inode: num, Size: ino.Size})
		}
	}
	sort.Slice(sounds, func(i, j int) bool { return sounds[i].Name < sounds[j].Name })
	return sounds, nil
}

// FindMap picks the maps/*.conf whose "# models:" line lists this model
// (prefix match, so GT-I9300 also covers GT-I9300T).
func FindMap(model, mapsDir string) string {
	if model == "" || mapsDir == "" {
		return ""
	}
	confs, _ := filepath.Glob(filepath.Join(mapsDir, "*.conf"))
	sort.Strings(confs)
	for _, conf := range confs {
		data, err := os.ReadFile(conf)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToLower(line), "# models:") {
				continue
			}
			for _, m := range strings.Fields(line[len("# models:"):]) {
				if strings.HasPrefix(strings.ToUpper(model), strings.ToUpper(m)) {
					return conf
				}
			}
		}
	}
	return ""
}

var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// SafeName makes a name usable as a file name on Windows and Android.
func SafeName(name string) string { return unsafeChars.ReplaceAllString(name, "_") }

// Extract copies every sound into out/<category>/ and writes
// firmware-info.txt, .nomedia and the matching UI map. It returns how many
// sounds were saved.
func Extract(fs *Ext4, out, mapsDir string, log Logf) (int, error) {
	audioDir, err := FindAudioDir(fs)
	if err != nil {
		return 0, err
	}
	info := ReadInfo(fs)
	model := info.Model
	if model == "" {
		model = "unknown model"
	}
	log("Firmware: %s, Android %s, build %s", model, info.Android, info.Build)

	for _, c := range Categories {
		entries, _ := os.ReadDir(filepath.Join(out, c))
		for _, e := range entries {
			if !e.IsDir() && isAudio(e.Name()) {
				return 0, errorf("%s already has sounds. Use an empty output folder (-o).", filepath.Join(out, c))
			}
		}
	}

	total := 0
	for _, c := range Categories {
		sounds, err := AudioFiles(fs, audioDir, c)
		if err != nil {
			return total, err
		}
		dir := filepath.Join(out, c)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return total, err
		}
		for _, s := range sounds {
			ino, err := fs.Inode(s.Inode)
			if err != nil {
				return total, err
			}
			data, err := fs.ReadFile(ino)
			if err != nil {
				return total, err
			}
			if err := os.WriteFile(filepath.Join(dir, SafeName(s.Name)), data, 0o644); err != nil {
				return total, err
			}
		}
		log("  %-14s %d sounds", c, len(sounds))
		total += len(sounds)
	}

	// Keep the sounds out of music players, galleries and "recent files"
	if err := os.WriteFile(filepath.Join(out, ".nomedia"), nil, 0o644); err != nil {
		return total, err
	}
	if err := os.WriteFile(filepath.Join(out, "firmware-info.txt"), []byte(info.String()+"\n"), 0o644); err != nil {
		return total, err
	}
	if conf := FindMap(info.Model, mapsDir); conf != "" {
		data, err := os.ReadFile(conf)
		if err != nil {
			return total, err
		}
		if err := os.WriteFile(filepath.Join(out, "ui-map.conf"), data, 0o644); err != nil {
			return total, err
		}
		log("UI map: %s", filepath.Base(conf))
	} else {
		log("UI map: none for this model, only same-name matching will be used")
	}
	return total, nil
}

// List prints every sound without extracting anything.
func List(fs *Ext4, log Logf) (int, error) {
	audioDir, err := FindAudioDir(fs)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range Categories {
		sounds, err := AudioFiles(fs, audioDir, c)
		if err != nil {
			return total, err
		}
		log("%s/ (%d)", c, len(sounds))
		for _, s := range sounds {
			log("  %-44s %7.1f KB", s.Name, float64(s.Size)/1024)
		}
		total += len(sounds)
	}
	return total, nil
}

// DefaultOutput is a new folder next to the firmware, named after its build.
func DefaultOutput(firmware string, info Info) string {
	build := info.Build
	if build == "" || build == "?" {
		build = strings.SplitN(filepath.Base(firmware), ".", 2)[0]
	}
	abs, err := filepath.Abs(firmware)
	if err != nil {
		abs = firmware
	}
	return filepath.Join(filepath.Dir(abs), SafeName("ClassicGalaxySounds-"+build))
}
