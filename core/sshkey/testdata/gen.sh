#!/usr/bin/env bash
# Regenerates the throwaway SSH key fixtures in this directory.
# These keys protect nothing; they exist only so the parsers are tested
# against files written by the real tools (OpenSSH, OpenSSL, KeePassXC).
#
# Needs: ssh-keygen (OpenSSH >= 9.x) and openssl (3.x). On Windows the
# copies bundled with Git for Windows work; DSA needs an ssh-keygen that
# still supports it (Windows OpenSSH 9.5), set via KEYGEN_DSA.
set -euo pipefail
cd "$(dirname "$0")"

KEYGEN=${KEYGEN:-ssh-keygen}
KEYGEN_DSA=${KEYGEN_DSA:-$KEYGEN}
OPENSSL=${OPENSSL:-openssl}
PASS='correct horse battery'

rm -f openssh_* pem_* pkcs8_* der_* reject_* *.pub
kg() { "$KEYGEN" -q "$@" </dev/null; }

# OpenSSH format, plain and with every cipher ssh-keygen -Z accepts.
kg -t ed25519 -N '' -C 'fixture ed25519' -f openssh_ed25519
kg -t rsa -b 2048 -N '' -C 'fixture rsa2048' -f openssh_rsa2048
kg -t ecdsa -b 256 -N '' -C 'fixture ecdsa256' -f openssh_ecdsa256
kg -t ecdsa -b 384 -N '' -C 'fixture ecdsa384' -f openssh_ecdsa384
kg -t ecdsa -b 521 -N '' -C 'fixture ecdsa521' -f openssh_ecdsa521
for c in aes128-ctr aes192-ctr aes256-ctr aes128-cbc aes192-cbc aes256-cbc \
         aes128-gcm@openssh.com aes256-gcm@openssh.com chacha20-poly1305@openssh.com; do
  n=${c%%@*}
  kg -t ed25519 -N "$PASS" -Z "$c" -C "fixture ed25519 $n" -f "openssh_ed25519_$n"
done
kg -t rsa -b 3072 -N "$PASS" -C 'fixture rsa3072 enc' -f openssh_rsa3072_aes256-ctr
kg -t ecdsa -b 384 -N "$PASS" -C 'fixture ecdsa384 enc' -f openssh_ecdsa384_aes256-ctr

# Traditional PEM (PKCS#1 / SEC1), plain and with legacy Proc-Type encryption.
kg -t rsa -b 2048 -m PEM -N '' -f pem_rsa2048
kg -t ecdsa -b 256 -m PEM -N '' -f pem_ec256
"$OPENSSL" rsa -in pem_rsa2048 -traditional -aes128 -passout "pass:$PASS" -out pem_rsa2048_aes128 2>/dev/null
"$OPENSSL" rsa -in pem_rsa2048 -traditional -aes256 -passout "pass:$PASS" -out pem_rsa2048_aes256 2>/dev/null
"$OPENSSL" rsa -in pem_rsa2048 -traditional -des3 -passout "pass:$PASS" -out pem_rsa2048_des3 2>/dev/null
"$OPENSSL" ec -in pem_ec256 -aes256 -passout "pass:$PASS" -out pem_ec256_aes256 2>/dev/null

# PKCS#8, plain and PBES2 (PBKDF2 with several PRFs, scrypt, 3DES).
"$OPENSSL" genpkey -algorithm ED25519 -out pkcs8_ed25519 2>/dev/null
"$OPENSSL" genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out pkcs8_rsa2048 2>/dev/null
"$OPENSSL" genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-521 -out pkcs8_ec521 2>/dev/null
"$OPENSSL" pkcs8 -topk8 -in pkcs8_ed25519 -v2 aes-256-cbc -v2prf hmacWithSHA256 -passout "pass:$PASS" -out pkcs8_ed25519_pbkdf2-sha256
"$OPENSSL" pkcs8 -topk8 -in pkcs8_rsa2048 -v2 aes-128-cbc -v2prf hmacWithSHA1 -passout "pass:$PASS" -out pkcs8_rsa2048_pbkdf2-sha1
"$OPENSSL" pkcs8 -topk8 -in pkcs8_ec521 -v2 aes-192-cbc -v2prf hmacWithSHA512 -passout "pass:$PASS" -out pkcs8_ec521_pbkdf2-sha512
"$OPENSSL" pkcs8 -topk8 -in pkcs8_ed25519 -v2 des3 -passout "pass:$PASS" -out pkcs8_ed25519_des3
"$OPENSSL" pkcs8 -topk8 -in pkcs8_ed25519 -scrypt -passout "pass:$PASS" -out pkcs8_ed25519_scrypt

# Raw DER without PEM armour.
"$OPENSSL" pkcs8 -topk8 -nocrypt -in pkcs8_rsa2048 -outform DER -out der_pkcs8_rsa2048
"$OPENSSL" rsa -in pem_rsa2048 -traditional -outform DER -out der_pkcs1_rsa2048 2>/dev/null
"$OPENSSL" ec -in pem_ec256 -outform DER -out der_sec1_ec256 2>/dev/null

# Inputs that must be recognised and refused with a reason.
"$KEYGEN_DSA" -q -t dsa -N '' -C 'fixture dsa' -f reject_dsa_openssh </dev/null
"$OPENSSL" dsaparam -genkey -out reject_dsa_pem 1024 2>/dev/null
"$OPENSSL" pkcs8 -topk8 -in pkcs8_ed25519 -v1 PBE-SHA1-3DES -passout "pass:$PASS" -out reject_pbes1
"$OPENSSL" genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:1024 -out reject_rsa1024 2>/dev/null
"$OPENSSL" genpkey -algorithm EC -pkeyopt ec_paramgen_curve:secp256k1 -out reject_secp256k1 2>/dev/null
cp openssh_ed25519.pub reject_public_openssh
"$KEYGEN" -e -f openssh_ed25519.pub > reject_public_rfc4716
"$OPENSSL" pkey -in pkcs8_rsa2048 -pubout -out reject_public_pem
kg -s openssh_ed25519 -I fixture-cert -n fixture openssh_ecdsa256.pub
mv openssh_ecdsa256-cert.pub reject_certificate
printf -- '---- BEGIN SSH2 ENCRYPTED PRIVATE KEY ----\nComment: "fixture sshcom"\nP2/56wAAAI8AAAA3aWYtbW9kbntzaWdue3JzYS1wa2NzMS1zaGExfSxlbmNyeXB0e3JzYS1wa2NzMXYyLW9hZXB9fQAAAARub25lAAAAQAAAADkAAAAA\n---- END SSH2 ENCRYPTED PRIVATE KEY ----\n' > reject_sshcom

# Public halves are only kept for keys the tests compare against.
rm -f pem_*.pub reject_*.pub

# Expected public keys: "<file> <ssh public key line without comment>".
# ssh-keygen -y decrypts with the shared passphrase where needed.
: > expected.txt
for f in openssh_* pem_* pkcs8_* der_*; do
  case "$f" in *.pub) continue ;; esac
  case "$f" in der_*) continue ;; esac
  pub=$("$KEYGEN" -y -P "$PASS" -f "$f" 2>/dev/null || "$KEYGEN" -y -P '' -f "$f")
  echo "$f $(echo "$pub" | awk '{print $1" "$2}')" >> expected.txt
done
rm -f openssh_*.pub
