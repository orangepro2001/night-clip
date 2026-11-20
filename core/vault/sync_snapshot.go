package vault

import (
	g "github.com/tobischo/gokeepasslib/v3"
)

type SyncSnapshot struct {
	FileHash string
	Upload   []byte
	Merged   bool
}

// PrepareSync merges a remote snapshot with the independently unlocked
// local vault. It records the exact local generation represented by its
// output so a simultaneous edit can't be marked as synchronized.
func (v *Vault) PrepareSync(remote, key []byte, baseline string, allowUpload bool) (SyncSnapshot, error) {
	var src *g.Database
	credentials := Creds{Password: key}
	if len(remote) > 0 {
		if err := CheckPeerKDF(remote); err != nil {
			return SyncSnapshot{}, err
		}
		var err error
		src, err = decode(remote, credentials)
		if err != nil {
			return SyncSnapshot{}, err
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return SyncSnapshot{}, err
	}
	current, _, ok := FileHash(v.dir)
	if !ok {
		return SyncSnapshot{}, ErrNotFound
	}
	out := SyncSnapshot{FileHash: current}
	if src != nil {
		stats, err := v.mergeSourceLocked(src, "sync", MergeOptions{})
		if err != nil {
			return SyncSnapshot{}, err
		}
		out.Merged = stats.Changed()
		if out.Merged {
			out.FileHash, _, ok = FileHash(v.dir)
			if !ok {
				return out, ErrNotFound
			}
		}
	}
	if allowUpload && current != baseline {
		var err error
		out.Upload, err = v.exportKDBXLocked(credentials, KDFStandard)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
