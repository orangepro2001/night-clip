package vault

import (
	"strings"

	g "github.com/tobischo/gokeepasslib/v3"

	"nightclip/core/sshkey"
)

// maxSSHKeyBytes caps the key attachment the agent loader will read. Real
// keys are a few KiB; RSA 16384 in OpenSSH format is about 13 KiB.
const maxSSHKeyBytes = 64 << 10

// NamedData is an attachment to store with a new entry.
type NamedData struct {
	Name string
	Data []byte
}

// AddEntryWithAttachments stores a new entry together with its attachments
// in one save, without the history items AddAttachment would create.
func (v *Vault) AddEntryWithAttachments(e Entry, atts []NamedData) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return "", err
	}
	if err := e.validate(); err != nil {
		return "", err
	}
	if v.countEntries() >= MaxEntries {
		return "", ErrTooLarge
	}
	if len(atts) > 32 {
		return "", ErrTooLarge
	}
	seen := map[string]bool{}
	for i, a := range atts {
		name, err := cleanAttachmentName(a.Name)
		if err != nil {
			return "", err
		}
		if seen[name] {
			return "", invalid("duplicate attachment %q", name)
		}
		seen[name] = true
		if len(a.Data) > MaxAttachmentBytes {
			return "", ErrTooLarge
		}
		atts[i].Name = name
	}
	gr := v.root()
	if e.GroupID != "" {
		var trash bool
		gr, _, trash = v.findGroup(e.GroupID)
		if gr == nil || trash {
			return "", ErrNotFound
		}
	}
	le := g.NewEntry()
	v.applyEntry(&le, e)
	for _, a := range atts {
		bin := v.db.AddBinary(append([]byte(nil), a.Data...))
		le.Binaries = append(le.Binaries, bin.CreateReference(a.Name))
	}
	now := v.nowWrap()
	le.Times.CreationTime, le.Times.LastModificationTime, le.Times.LastAccessTime, le.Times.LocationChanged = &now, &now, &now, &now
	gr.Entries = append(gr.Entries, le)
	return uuidHex(le.UUID), v.commit("save")
}

// ReplaceAttachments sets (or, with nil data, removes) several attachments
// of an entry in one save with a single history item.
func (v *Vault) ReplaceAttachments(id string, atts []NamedData) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return ErrNotFound
	}
	for i, a := range atts {
		name, err := cleanAttachmentName(a.Name)
		if err != nil {
			return err
		}
		if len(a.Data) > MaxAttachmentBytes {
			return ErrTooLarge
		}
		atts[i].Name = name
	}
	v.pushHistory(le)
	for _, a := range atts {
		out := le.Binaries[:0]
		for _, b := range le.Binaries {
			if b.Name != a.Name {
				out = append(out, b)
			}
		}
		le.Binaries = out
		if a.Data != nil {
			bin := v.db.AddBinary(append([]byte(nil), a.Data...))
			le.Binaries = append(le.Binaries, bin.CreateReference(a.Name))
		}
	}
	if len(le.Binaries) > 32 {
		return ErrTooLarge
	}
	v.touch(le)
	return v.commit("save")
}

// SSHCandidate is an entry that holds (or claims to hold) an SSH key, with
// copies of everything the agent loader needs. The byte slices are copies:
// the library hands out its internal buffers, which must never be modified
// or zeroed.
type SSHCandidate struct {
	ID        string
	Title     string
	GroupID   string
	Kind      string
	InTrash   bool
	ExpiresAt int64
	Password  string // KeePassXC keeps an encrypted key's passphrase here
	Settings  []byte // KeeAgent.settings; nil when absent
	KeyName   string // the attachment holding the key; empty when unknown
	Key       []byte // copy of that attachment; nil when missing or too large
	KeySize   int    // size of the attachment, even when too large to copy
	Hosts     []string
}

// cdSSHHosts holds the hosts an SSH key is limited to, one pattern per line.
// KeePassXC keeps CustomData it does not know, so the binding survives a
// round trip through it (KeePassXC itself does not enforce it).
const cdSSHHosts = "ClipBridge.SSH.Hosts"

func splitHosts(v string) []string {
	var out []string
	for _, h := range strings.Split(v, "\n") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// SetSSHHosts limits an SSH key entry to the given hosts (none = any host).
// Patterns are stored as given; the facade validates them. The change is
// metadata: it does not add a history item.
func (v *Vault) SetSSHHosts(id string, hosts []string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return ErrNotFound
	}
	if len(hosts) > 32 {
		return ErrTooLarge
	}
	value := strings.Join(hosts, "\n")
	if getCustom(le, cdSSHHosts) == value {
		return nil
	}
	if value == "" {
		delCustom(le, cdSSHHosts)
	} else {
		setCustom(le, cdSSHHosts, value)
	}
	v.touch(le)
	return v.commit("save")
}

// SSHCandidates returns the entries that carry KeeAgent.settings or have
// the ssh kind, including those in the trash (callers skip them). id ""
// returns all of them.
func (v *Vault) SSHCandidates(id string) ([]SSHCandidate, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	out := []SSHCandidate{}
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		if id != "" && uuidHex(le.UUID) != id {
			return
		}
		kind := entryKind(le)
		if kind != KindSSH {
			return
		}
		c := SSHCandidate{
			ID: uuidHex(le.UUID), Title: entryGet(le, fTitle), GroupID: gid, Kind: kind, InTrash: trash,
			Password: entryGet(le, fPassword), Hosts: splitHosts(getCustom(le, cdSSHHosts)),
		}
		if le.Times.Expires.Bool {
			c.ExpiresAt = unix(le.Times.ExpiryTime)
		}
		if b := v.binary(le, sshkey.SettingsAttachment); b != nil {
			c.Settings = append([]byte(nil), b...)
		}
		if c.Settings != nil {
			if s, err := sshkey.ParseAgentSettings(c.Settings); err == nil && s.LocationType == "attachment" {
				c.KeyName = s.AttachmentName
			}
		} else {
			// No settings: an ssh entry whose only other attachment is the key.
			var names []string
			for _, br := range le.Binaries {
				if br.Name != sshkey.SettingsAttachment {
					names = append(names, br.Name)
				}
			}
			if len(names) == 1 {
				c.KeyName = names[0]
			}
		}
		if c.KeyName != "" {
			if b := v.binary(le, c.KeyName); b != nil {
				c.KeySize = len(b)
				if len(b) <= maxSSHKeyBytes {
					c.Key = append([]byte(nil), b...)
				}
			}
		}
		out = append(out, c)
	}, nil)
	return out, nil
}

// binary returns the library's buffer for an attachment (do not modify it).
func (v *Vault) binary(le *g.Entry, name string) []byte {
	for _, br := range le.Binaries {
		if br.Name != name {
			continue
		}
		b := v.db.FindBinary(br.Value.ID)
		if b == nil {
			return nil
		}
		c, err := b.GetContentBytes()
		if err != nil {
			return nil
		}
		return c
	}
	return nil
}
