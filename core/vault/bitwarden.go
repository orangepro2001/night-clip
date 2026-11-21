package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	g "github.com/tobischo/gokeepasslib/v3"
)

var ErrBitwardenEncrypted = errors.New("encrypted Bitwarden export")

type BitwardenImportResult struct {
	Added           int `json:"added"`
	Skipped         int `json:"skipped"`
	PasskeysSkipped int `json:"passkeys_skipped"`
}

type bitwardenExport struct {
	Encrypted bool
	Folders   []struct{ ID, Name string }
	Items     []bitwardenItem
}

type bitwardenItem struct {
	Type                  int
	Name, Notes, FolderID string
	DeletedDate           string
	Fields                []struct {
		Name           string
		Value          json.RawMessage
		Type, LinkedID int
	}
	Login *struct {
		Username, Password, TOTP string
		URIs                     []struct {
			URI   string
			Match *int
		}
		FIDO2Credentials []json.RawMessage
	}
	Card, Identity, SSHKey map[string]*string
}

// ImportBitwardenJSON adds entries from a plaintext Bitwarden export and saves the vault once.
func (v *Vault) ImportBitwardenJSON(in io.Reader) (BitwardenImportResult, error) {
	const maxJSONBytes = 64 << 20
	var result BitwardenImportResult
	data, err := io.ReadAll(io.LimitReader(in, maxJSONBytes+1))
	if err != nil {
		return result, invalid("cannot read Bitwarden JSON")
	}
	if len(data) > maxJSONBytes {
		return result, ErrTooLarge
	}
	var src bitwardenExport
	if json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &src) != nil {
		return result, invalid("invalid Bitwarden JSON")
	}
	if src.Encrypted {
		return result, ErrBitwardenEncrypted
	}
	if src.Items == nil {
		return result, invalid("Bitwarden JSON needs an items array")
	}

	folders := make(map[string]string, len(src.Folders))
	for _, folder := range src.Folders {
		if folder.ID != "" {
			folders[folder.ID] = folder.Name
		}
	}
	type pendingEntry struct {
		entry  Entry
		folder string
	}
	var pending []pendingEntry
	for _, item := range src.Items {
		e, ok := item.entry()
		if !ok || e.validate() != nil {
			result.Skipped++
			continue
		}
		if item.Login != nil {
			result.PasskeysSkipped += len(item.Login.FIDO2Credentials)
		}
		pending = append(pending, pendingEntry{entry: e, folder: folders[item.FolderID]})
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return BitwardenImportResult{}, err
	}
	if len(pending) == 0 {
		return result, nil
	}
	if len(pending) > MaxEntries-v.countEntries() {
		return BitwardenImportResult{}, ErrTooLarge
	}

	// Import only appends to groups. Copy the group tree so a failed save leaves the open vault intact.
	oldGroups := v.db.Content.Root.Groups
	v.db.Content.Root.Groups = copyImportGroups(oldGroups)
	groupFor := v.importFolderLookup()
	now := v.nowWrap()
	for _, p := range pending {
		gid := groupFor(p.folder)
		gr := v.root()
		if gid != "" {
			gr, _, _ = v.findGroup(gid)
		}
		le := newEntry()
		v.applyEntry(&le, p.entry)
		le.Times.CreationTime, le.Times.LastModificationTime, le.Times.LastAccessTime, le.Times.LocationChanged = &now, &now, &now, &now
		gr.Entries = append(gr.Entries, le)
	}
	if err := v.commit("import"); err != nil {
		v.db.Content.Root.Groups = oldGroups
		return BitwardenImportResult{}, err
	}
	result.Added = len(pending)
	return result, nil
}

func copyImportGroups(groups []g.Group) []g.Group {
	next := slices.Clone(groups)
	for i := range next {
		gr := &next[i]
		gr.Groups = copyImportGroups(gr.Groups)
		gr.Entries = slices.Clip(gr.Entries)
	}
	return next
}

func (item bitwardenItem) entry() (Entry, bool) {
	e := Entry{Title: item.Name, Notes: item.Notes}
	if item.DeletedDate != "" {
		return e, false
	}
	var attributes map[string]*string
	var label string
	switch item.Type {
	case 1:
		if item.Login == nil {
			return e, false
		}
		e.Kind = KindLogin
		e.Username, e.Password, e.TOTP = item.Login.Username, item.Login.Password, item.Login.TOTP
		for i, u := range item.Login.URIs {
			if strings.TrimSpace(u.URI) == "" {
				continue
			}
			// The default/domain policy maps to the vault's web matcher. Keep other match
			// modes as visible data so rules such as Never cannot become autofill targets.
			if u.Match != nil && *u.Match != 0 {
				e.Fields = append(e.Fields, Field{Name: fmt.Sprintf("Bitwarden URI %d (match %d)", i+1, *u.Match), Value: u.URI})
			} else if e.URL == "" {
				e.URL = u.URI
			} else {
				e.Fields = append(e.Fields, Field{Name: fmt.Sprintf("URL %d", i+1), Value: u.URI})
			}
		}
		if e.TOTP != "" {
			if _, err := ParseTOTP(e.TOTP); err != nil {
				e.Fields = append(e.Fields, Field{Name: "Bitwarden TOTP", Value: e.TOTP, Protected: true})
				e.TOTP = ""
			}
		}
	case 2:
		e.Kind = KindNote
	case 3, 4, 5:
		// These are readable as secure notes; only login entries participate in autofill.
		e.Kind = KindNote
		switch item.Type {
		case 3:
			attributes, label = item.Card, "Card"
		case 4:
			attributes, label = item.Identity, "Identity"
		case 5:
			attributes, label = item.SSHKey, "SSH key"
		}
		if attributes == nil {
			return e, false
		}
	default:
		return e, false
	}

	used := make(map[string]bool)
	for _, f := range e.Fields {
		used[strings.ToLower(f.Name)] = true
	}
	add := func(name, value string, protected bool) {
		name = strings.TrimSpace(name)
		lower := strings.ToLower(name)
		switch {
		case name == "":
			name = "Bitwarden field"
		case reserved[lower] || strings.HasPrefix(lower, "kph:") || isAutofillURLField(lower) || lower == "androidapp":
			// A user-named field is data, even when its name looks like a KeePass system field.
			name = "Bitwarden: " + name
		}
		base := name
		index := 1
		for {
			if len(name) > 128 {
				// Keep both ends: generated numeric suffixes must survive truncation.
				suffix := ""
				if index > 1 {
					suffix = fmt.Sprintf(" (%d)", index)
				}
				name = base[:128-len(suffix)]
				for !utf8.ValidString(name) {
					name = name[:len(name)-1]
				}
				name += suffix
			}
			key := strings.ToLower(name)
			if !used[key] {
				used[key] = true
				e.Fields = append(e.Fields, Field{Name: name, Value: value, Protected: protected})
				return
			}
			index++
			name = fmt.Sprintf("%s (%d)", base, index)
		}
	}
	names := make([]string, 0, len(attributes))
	for name := range attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if value := attributes[name]; value != nil {
			add(label+": "+name, *value, true)
		}
	}

	for _, field := range item.Fields {
		value := bitwardenFieldValue(field.Value)
		protected := field.Type == 1
		if field.Type == 3 {
			switch {
			case item.Login != nil && field.LinkedID == 100:
				value = e.Username
			case item.Login != nil && field.LinkedID == 101:
				value, protected = e.Password, true
			case value == "":
				value = fmt.Sprintf("Bitwarden linked field %d", field.LinkedID)
			}
		}
		add(field.Name, value, protected)
	}
	return e, true
}

func bitwardenFieldValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	// Bitwarden normally encodes booleans as strings. Also accept JSON booleans.
	return string(raw)
}
