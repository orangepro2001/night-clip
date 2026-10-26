package vault

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	g "github.com/tobischo/gokeepasslib/v3"
)

// Limits on the shape of a decoded database. They bound the work the merge, the XML encoder and the
// listings do, so a hostile file (a peer that knows the password, an import) cannot freeze or exhaust
// the app: the decoder cap alone only bounds the bytes, not how they are structured.
const (
	maxGroupDepth    = 16
	maxGroups        = 2000
	maxHistoryItems  = 100
	maxAttachments   = 64
	maxTotalTextSize = 64 << 20 // all text of all entries and history items
	maxFieldDecoded  = 4 << 20  // one text field of a file we did not write (KeePassXC notes can be long)
)

func init() {
	// The library caps gzip output at 256 MiB; a vault file may be at most 64 MiB, and real content
	// compresses a few times, not hundreds of times.
	g.MaxDecompressedBytes = 3 * MaxFileBytes
}

// shapeError describes why a database was refused.
func shapeError(format string, a ...any) error {
	return fmt.Errorf("%w: the database is too complex: %s", ErrUnsupported, fmt.Sprintf(format, a...))
}

// checkShape validates a decoded database and repairs what can be repaired in place: attachment names
// that could be mistaken for paths are made harmless.
func checkShape(db *g.Database) error {
	groups, entries := 0, 0
	var total int64
	var rec func(gr *g.Group, depth int) error
	rec = func(gr *g.Group, depth int) error {
		if depth > maxGroupDepth {
			return shapeError("groups are nested more than %d deep", maxGroupDepth)
		}
		groups++
		if groups > maxGroups {
			return shapeError("more than %d groups", maxGroups)
		}
		var chk func(e *g.Entry, history bool) error
		chk = func(e *g.Entry, history bool) error {
			if len(e.Binaries) > maxAttachments || len(e.Values) > 500 {
				return shapeError("an entry has too many attachments or fields")
			}
			for _, v := range e.Values {
				n := len(v.Key) + len(v.Value.Content)
				if len(v.Value.Content) > maxFieldDecoded || len(v.Key) > 4096 {
					return shapeError("a field is larger than %d bytes", maxFieldDecoded)
				}
				total += int64(n)
			}
			total += int64(len(e.Tags))
			if total > maxTotalTextSize {
				return shapeError("more than %d bytes of text", maxTotalTextSize)
			}
			for i := range e.Binaries {
				e.Binaries[i].Name = safeAttachmentName(e.Binaries[i].Name)
			}
			if !history {
				n := 0
				for _, h := range e.Histories {
					n += len(h.Entries)
					for i := range h.Entries {
						if err := chk(&h.Entries[i], true); err != nil {
							return err
						}
					}
				}
				if n > maxHistoryItems {
					return shapeError("an entry has more than %d history items", maxHistoryItems)
				}
			}
			return nil
		}
		for i := range gr.Entries {
			entries++
			if entries > MaxEntries {
				return shapeError("more than %d entries", MaxEntries)
			}
			if err := chk(&gr.Entries[i], false); err != nil {
				return err
			}
		}
		for i := range gr.Groups {
			if err := rec(&gr.Groups[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if len(db.Content.Root.Groups) != 1 {
		return shapeError("the database must have exactly one top group")
	}
	return rec(&db.Content.Root.Groups[0], 1)
}

// safeAttachmentName turns a name that came from outside into one that cannot be used as a path.
func safeAttachmentName(n string) string {
	var b strings.Builder
	for _, r := range n {
		switch {
		case r == '/' || r == '\\' || r == ':' || r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, ".")
	if out == "" {
		out = "attachment"
	}
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

// kdfCost is what a file header asks the key derivation to spend.
type kdfCost struct {
	argon2      bool
	memoryBytes uint64
	iterations  uint64
	parallelism uint32
	aesRounds   uint64
}

// peekKDF reads the key derivation parameters out of a KDBX 4 header without deriving anything.
func peekKDF(data []byte) (kdfCost, bool) {
	var c kdfCost
	if len(data) < 12 || !bytes.Equal(data[:8], kdbxSignature) || data[10] != 4 && data[10] != 3 {
		return c, false
	}
	if data[10] == 3 { // KDBX 3.x: AES-KDF with the rounds in header field 6
		p := 12
		for p+3 <= len(data) {
			id := data[p]
			n := int(binary.LittleEndian.Uint16(data[p+1:]))
			p += 3
			if p+n > len(data) {
				return c, false
			}
			if id == 6 && n == 8 {
				c.aesRounds = binary.LittleEndian.Uint64(data[p:])
				return c, true
			}
			if id == 0 {
				return c, false
			}
			p += n
		}
		return c, false
	}
	p := 12
	for p+5 <= len(data) {
		id := data[p]
		n := int(binary.LittleEndian.Uint32(data[p+1:]))
		p += 5
		if n < 0 || p+n > len(data) {
			return c, false
		}
		if id == 11 { // KdfParameters, a variant dictionary
			d := data[p : p+n]
			if len(d) < 3 {
				return c, false
			}
			q := 2
			for q < len(d) && d[q] != 0 {
				typ := d[q]
				if q+5 > len(d) {
					return c, false
				}
				kl := int(binary.LittleEndian.Uint32(d[q+1:]))
				if kl < 0 || q+5+kl+4 > len(d) {
					return c, false
				}
				key := string(d[q+5 : q+5+kl])
				vl := int(binary.LittleEndian.Uint32(d[q+5+kl:]))
				vo := q + 5 + kl + 4
				if vl < 0 || vo+vl > len(d) {
					return c, false
				}
				val := d[vo : vo+vl]
				u64 := func() uint64 {
					if len(val) == 8 {
						return binary.LittleEndian.Uint64(val)
					}
					return 0
				}
				switch key {
				case "$UUID":
					c.argon2 = bytes.Equal(val, kdfArgon2d[:])
				case "M":
					if typ == 0x05 {
						c.memoryBytes = u64()
					}
				case "I":
					if typ == 0x05 {
						c.iterations = u64()
					}
				case "P":
					if typ == 0x04 && len(val) == 4 {
						c.parallelism = binary.LittleEndian.Uint32(val)
					}
				case "R":
					if typ == 0x05 {
						c.aesRounds = u64()
					}
				}
				q = vo + vl
			}
			return c, true
		}
		if id == 0 {
			return c, false
		}
		p += n
	}
	return c, false
}

var (
	kdbxSignature = []byte{0x03, 0xd9, 0xa2, 0x9a, 0x67, 0xfb, 0x4b, 0xb5}
	kdfArgon2d    = [16]byte{0xEF, 0x63, 0x6D, 0xDF, 0x8C, 0x29, 0x44, 0x4B, 0x91, 0xF7, 0xA9, 0xA4, 0x03, 0xE3, 0x0A, 0x0C}
)

// Limits for files that arrive from another device. Our own files use what the user chose (64 MiB
// by default); a peer has no business asking for more than this before it has proven anything.
const (
	peerMaxArgonMemory = 128 << 20
	peerMaxArgonIter   = 16
	peerMaxArgonPar    = 16
	peerMaxAESRounds   = 20_000_000
)

// CheckPeerKDF refuses a file whose header demands a key derivation cost that only an attacker would
// ask for, before any derivation runs (the header is authenticated only after it).
func CheckPeerKDF(data []byte) error {
	c, ok := peekKDF(data)
	if !ok {
		return fmt.Errorf("%w: unreadable file header", ErrUnsupported)
	}
	if c.argon2 {
		if c.memoryBytes > peerMaxArgonMemory || c.iterations > peerMaxArgonIter || uint64(c.parallelism) > peerMaxArgonPar || c.iterations < 1 || c.parallelism < 1 {
			return fmt.Errorf("%w: key derivation cost is outside what a synced file may ask for", ErrUnsupported)
		}
		return nil
	}
	if c.aesRounds > peerMaxAESRounds {
		return fmt.Errorf("%w: key derivation cost is outside what a synced file may ask for", ErrUnsupported)
	}
	return nil
}

// kdfFloorOn is switched off by the unit tests, which create vaults with deliberately cheap parameters.
var kdfFloorOn = true

// weakKDF reports whether a database's key derivation is below the floor we keep: Argon2d with at
// least 16 MiB and 2 iterations. AES-KDF (older KeePass) counts as weak.
func weakKDF(db *g.Database) bool {
	if !kdfFloorOn {
		return false
	}
	kp := db.Header.FileHeaders.KdfParameters
	if kp == nil || !bytes.Equal(kp.UUID, g.KdfArgon2) {
		return true
	}
	return kp.Memory < 16<<20 || kp.Iterations < 2
}

// raiseKDF sets the key derivation to the standard Argon2d cost (the next save uses it).
func raiseKDF(db *g.Database) {
	kp := db.Header.FileHeaders.KdfParameters
	if kp == nil {
		kp = &g.KdfParameters{}
		db.Header.FileHeaders.KdfParameters = kp
	}
	kp.UUID = g.KdfArgon2
	kp.Rounds = 0
	kp.Memory = KDFStandard.MemoryMiB << 20
	kp.Iterations = KDFStandard.Iterations
	kp.Parallelism = KDFStandard.Parallelism
	if kp.Version == 0 {
		kp.Version = 0x13
	}
}
