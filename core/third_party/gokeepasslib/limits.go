package gokeepasslib

import "errors"

// Resource limits added by the night-clip vendored copy (see PATCHES.md). Upstream trusts
// every number in the file header, and the header is only authenticated *after* the key
// derivation, so a few hundred bytes of attacker-chosen header could demand gigabytes of
// memory or an endless loop. The values are variables so tests (and callers that know better)
// can change them; the defaults are generous for real databases and fatal for hostile ones.
var (
	// MaxArgon2MemoryBytes caps the Argon2 memory parameter.
	MaxArgon2MemoryBytes uint64 = 256 << 20
	// MaxArgon2Iterations caps the Argon2 iteration count.
	MaxArgon2Iterations uint64 = 256
	// MaxArgon2Parallelism caps the Argon2 lane count.
	MaxArgon2Parallelism uint32 = 64
	// MaxArgon2Work caps memory(MiB) * iterations, i.e. the total time the derivation can take.
	MaxArgon2Work uint64 = 8192
	// MaxAESKDFRounds caps the AES-KDF round count (KeePass benchmarks about 6e6 rounds per second).
	MaxAESKDFRounds uint64 = 100_000_000
	// MaxDecompressedBytes caps what a gzip stream inside the database may expand to.
	MaxDecompressedBytes int64 = 256 << 20
)

// ErrLimitExceeded is returned when a database asks for more than the limits above allow.
var ErrLimitExceeded = errors.New("gokeepasslib: database key-derivation or size parameters exceed the configured limits")

// ErrUnsupportedKDF is returned for a key derivation function this library cannot perform. Upstream
// silently falls back to AES-KDF with whatever rounds the file says (zero for an Argon2id file),
// which would "derive" a key without any stretching.
var ErrUnsupportedKDF = errors.New("gokeepasslib: unsupported key derivation function")
