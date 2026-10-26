# Vendored gokeepasslib v3.7.1 (MIT)

Upstream: https://github.com/tobischo/gokeepasslib (see LICENSE.md). Copied without tests, examples
and fixtures. Changes made for night-clip (docs/vault-v0-poc-report.md has the evidence for each):

1. `limits.go` (new): caps for Argon2 memory/iterations/parallelism/total work, AES-KDF rounds and
   gzip expansion; `ErrLimitExceeded`, `ErrUnsupportedKDF`.
2. `credentials.go`: the key derivation is checked against those limits *before* it runs (the header
   is only authenticated afterwards), and an unknown KDF UUID is an error instead of a silent fall
   back to AES-KDF with whatever rounds the header says. Argon2 memory below 8 KiB is rejected.
3. `decoder.go`, `binary.go`: gzip output is limited (decompression bombs).
4. `content.go`: the inner-header "random stream id" field must be at least 4 bytes (upstream panics).

5. `binary.go`: `GetContentBytes` returns a KDBX 4 binary (flagged as such when read) as it is. Upstream tried base64 first for every binary, so an attachment whose bytes are
   valid base64 (for example the text `data`) came back as different bytes.

Not changed on purpose: the writer still appends 36 zero bytes after the terminating block and pads
ChaCha20 payloads; KeePassXC 2.7.10 reads those files (checked in core/vault interop tests). The
library re-uses the opened file's MasterSeed/IV/KDF salt/inner stream key when saving; core/vault
regenerates them on every save (`reseed`). Argon2id is not supported (rejected, not faked).
