package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nightclip/core/proto"
)

// PublishOptions describes one release to publish into a release directory.
type PublishOptions struct {
	Dir         string // release root, e.g. ~/.local/share/clipbridge/releases
	Platform    string
	Source      string // the .apk (android) or .zip (windows) to publish
	VersionCode int64
	VersionName string
	Notes       string
	Force       bool // raise minVersionCode to this release: older apps must update
	Key         ed25519.PrivateKey
	Keep        int // how many packages to retain (default 3)
	Now         func() time.Time
}

var packageExt = map[string]string{PlatformAndroid: ".apk", PlatformWindows: ".zip"}

// Publish copies the package into <Dir>/<platform>/files, then atomically
// replaces <Dir>/<platform>/latest.json with a freshly signed manifest. It
// refuses a versionCode that is not higher than the published one.
func Publish(opt PublishOptions) (*Manifest, error) {
	if !ValidPlatform(opt.Platform) {
		return nil, proto.Errorf(proto.CodeInvalid, "platform must be android or windows")
	}
	ext := packageExt[opt.Platform]
	if !strings.EqualFold(filepath.Ext(opt.Source), ext) {
		return nil, proto.Errorf(proto.CodeInvalid, "%s releases must be a %s file", opt.Platform, ext)
	}
	if opt.Keep <= 0 {
		opt.Keep = 3
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	platDir := filepath.Join(opt.Dir, opt.Platform)
	filesDir := filepath.Join(platDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, err
	}

	prev := currentManifest(opt.Dir, opt.Platform)
	minCode := int64(0)
	if prev != nil {
		if opt.VersionCode <= prev.VersionCode {
			return nil, proto.Errorf(proto.CodeInvalid,
				"versionCode %d is not higher than the published %d", opt.VersionCode, prev.VersionCode)
		}
		minCode = prev.MinVersionCode
	}
	if opt.Force {
		minCode = opt.VersionCode
	}

	name := fmt.Sprintf("clipbridge-%s-%s-%d%s", opt.Platform, opt.VersionName, opt.VersionCode, ext)
	if !ValidFileName(name) {
		return nil, proto.Errorf(proto.CodeInvalid, "versionName %q cannot be part of a file name", opt.VersionName)
	}
	sum, size, err := copyAtomic(opt.Source, filepath.Join(filesDir, name))
	if err != nil {
		return nil, err
	}

	m := &Manifest{
		Platform: opt.Platform, VersionCode: opt.VersionCode, VersionName: opt.VersionName,
		File: name, Size: size, SHA256: sum, MinVersionCode: minCode, Notes: opt.Notes,
		PublishedAt: opt.Now().UTC().Format(time.RFC3339),
	}
	if err := m.Sign(opt.Key); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(platDir, "latest.json"), append(raw, '\n')); err != nil {
		return nil, err
	}
	pruneOld(filesDir, name, opt.Keep)
	return m, nil
}

func copyAtomic(src, dst string) (sum string, size int64, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// pruneOld keeps the newest `keep` packages (always including current).
func pruneOld(dir, current string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type f struct {
		name string
		mod  time.Time
	}
	var files []f
	for _, e := range entries {
		if e.IsDir() || e.Name() == current || !ValidFileName(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, f{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i := keep - 1; i < len(files); i++ { // current already counts as one
		_ = os.Remove(filepath.Join(dir, files[i].name))
	}
}

// VerifyPublished checks that the published manifest of a platform carries a
// trusted signature and that the file it names matches its size and SHA-256.
func VerifyPublished(dir, platform string, keys []ed25519.PublicKey) (*Manifest, error) {
	if !ValidPlatform(platform) {
		return nil, proto.Errorf(proto.CodeInvalid, "platform must be android or windows")
	}
	raw, err := os.ReadFile(filepath.Join(dir, platform, "latest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, rejected("not valid JSON")
	}
	if err := m.Verify(keys); err != nil {
		return nil, err
	}
	if err := VerifyFile(filepath.Join(dir, platform, "files", m.File), &m); err != nil {
		return nil, err
	}
	return &m, nil
}
