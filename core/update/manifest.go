// Package update implements ClipBridge's self-update against the local
// release-publish service: a client that checks, watches and downloads with
// resume and verification (the signed manifest and the wire protocol belong to
// release-publish's SDK, vendored in third_party/relhub), and the background
// manager the facade drives. See docs/update.md.
package update

import "relhub/pkg/relproto"

// AppID is the id ClipBridge is registered under in release-publish.
const AppID = "clipbridge"

// Platforms that can be updated. They are the platform names used in
// release-publish (`relhub publish -platform ...`) and the values of the
// `platform` core setting.
const (
	PlatformAndroid = "android"
	PlatformWindows = "windows"
	PlatformMacOS   = "macos"
)

// MaxPackageBytes bounds what a manifest may announce. release-publish allows
// 16 GiB; a signed manifest is trusted, but a typo must not fill a phone.
const MaxPackageBytes = 512 << 20

// Manifest is the signed description of the release a channel offers, already
// verified by the client that returned it.
type Manifest = relproto.Manifest

// ValidPlatform reports whether p is an updatable platform.
func ValidPlatform(p string) bool {
	return p == PlatformAndroid || p == PlatformWindows || p == PlatformMacOS
}

// ValidFileName reports whether name is safe to use as a bare file name.
func ValidFileName(name string) bool { return relproto.ValidFileName(name) }
