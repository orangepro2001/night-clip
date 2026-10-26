package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Issue is one entry in a health report.
type Issue struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Info  string `json:"info,omitempty"` // for weak: the strength label; for reused: how many share it
}

// Health is the local password health report. Nothing leaves the device: strength is estimated here,
// reuse is found by comparing hashes in memory, there is no breach lookup.
type Health struct {
	Weak    []Issue `json:"weak"`
	Reused  []Issue `json:"reused"`
	Old     []Issue `json:"old"`     // password unchanged for more than a year
	Expired []Issue `json:"expired"` // past the expiry date
	NoTOTP  int     `json:"without_totp"`
	Total   int     `json:"total"`
}

const oldAfterSeconds = 365 * 24 * 3600

// Health analyses every entry that holds a password (notes are skipped, the recycle bin too).
func (v *Vault) Health() (Health, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return Health{}, err
	}
	now := v.now().Unix()
	h := Health{Weak: []Issue{}, Reused: []Issue{}, Old: []Issue{}, Expired: []Issue{}}
	byHash := map[string][]Issue{}
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		if trash {
			return
		}
		e := fromLib(v.db, le, gid, trash)
		if e.Kind == KindNote {
			return
		}
		h.Total++
		if e.ExpiresAt > 0 && e.ExpiresAt < now {
			h.Expired = append(h.Expired, Issue{ID: e.ID, Title: e.Title})
		}
		if e.Password == "" {
			return
		}
		if e.TOTP == "" && e.Kind == KindLogin {
			h.NoTOTP++
		}
		if s := Strength(e.Password); s.Label == "very_weak" || s.Label == "weak" {
			h.Weak = append(h.Weak, Issue{ID: e.ID, Title: e.Title, Info: s.Label})
		}
		if now-e.ModifiedAt > oldAfterSeconds {
			h.Old = append(h.Old, Issue{ID: e.ID, Title: e.Title})
		}
		sum := sha256.Sum256([]byte(e.Password))
		k := hex.EncodeToString(sum[:])
		byHash[k] = append(byHash[k], Issue{ID: e.ID, Title: e.Title})
	}, nil)
	for _, group := range byHash {
		if len(group) > 1 {
			for _, is := range group {
				is.Info = itoa(len(group))
				h.Reused = append(h.Reused, is)
			}
		}
	}
	for _, l := range [][]Issue{h.Weak, h.Reused, h.Old, h.Expired} {
		sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i].Title) < strings.ToLower(l[j].Title) })
	}
	return h, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
