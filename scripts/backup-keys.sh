#!/usr/bin/env bash
# Backs up the release signing material that exists ONLY on this machine:
#   ~/.config/clipbridge/release-ed25519.key        manifest signing key (all platforms)
#   ~/.config/clipbridge/clipbridge-release.jks     Android signing keystore
#   ~/.config/clipbridge/android-signing.properties keystore alias + passwords
# Losing the keystore means every installed Android app must be uninstalled and reinstalled;
# losing the Ed25519 key means shipping a new app build that trusts a new key.
#
#   scripts/backup-keys.sh [DEST_DIR]      default DEST_DIR: ~/backups/clipbridge-keys
#
# The archive is plaintext (mode 600 in a 700 directory): a copy on the same disk only
# protects against accidental deletion. Copy it OFF this machine too (password manager
# attachment, encrypted USB drive, ...). The archive is verified after writing.
set -euo pipefail
src="$HOME/.config/clipbridge"
dest="${1:-$HOME/backups/clipbridge-keys}"
files=(release-ed25519.key clipbridge-release.jks android-signing.properties)

for f in "${files[@]}"; do
  [ -f "$src/$f" ] || { echo "missing $src/$f" >&2; exit 1; }
done
umask 077
mkdir -p "$dest"
chmod 700 "$dest"
stamp="$(date +%Y%m%d-%H%M%S)"
archive="$dest/clipbridge-keys-$stamp.tar.gz"
tar -C "$src" -czf "$archive" "${files[@]}"

# Verify: unpack elsewhere and compare every file byte for byte.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
tar -C "$tmp" -xzf "$archive"
for f in "${files[@]}"; do
  cmp -s "$src/$f" "$tmp/$f" || { echo "verification failed for $f" >&2; rm -f "$archive"; exit 1; }
done

(cd "$dest" && sha256sum "$(basename "$archive")" >> SHA256SUMS)
cat > "$dest/RESTORE.txt" <<TXT
Restore (same user, same machine layout):
  mkdir -p ~/.config/clipbridge && chmod 700 ~/.config/clipbridge
  tar -C ~/.config/clipbridge -xzf clipbridge-keys-<stamp>.tar.gz
  chmod 600 ~/.config/clipbridge/*
Check:  sha256sum -c SHA256SUMS
android-signing.properties points at \$HOME/.config/clipbridge/clipbridge-release.jks;
edit storeFile if you restore elsewhere.
The Ed25519 public half is compiled into core/update/keys.go (a different key there = apps
reject your updates). Do NOT regenerate either key while installed apps exist.
TXT
chmod 600 "$archive" "$dest/SHA256SUMS" "$dest/RESTORE.txt"
echo "backup written and verified: $archive"
