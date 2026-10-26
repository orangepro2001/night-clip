package vault

import (
	"sort"
	"strings"

	g "github.com/tobischo/gokeepasslib/v3"
)

// Entry kinds. The kind only changes how a shell presents an entry; every kind is a normal KeePass
// entry (an API key is a login whose "password" is the key), so KeePassXC shows them all.
const (
	KindLogin  = "login"  // site / service account: username + password + URL
	KindAPIKey = "apikey" // API key or token: the key goes in Password, endpoint in URL, extras in Fields
	KindSecret = "secret" // a single secret value (license key, recovery code, PIN)
	KindNote   = "note"   // a secure note: only Notes
)

// Reserved KeePass field names; custom fields must not use them.
const (
	fTitle    = "Title"
	fUser     = "UserName"
	fPassword = "Password"
	fURL      = "URL"
	fNotes    = "Notes"
	fOTP      = "otp"

	cdKind      = "ClipBridge.Kind"
	cdPrevGroup = "ClipBridge.PrevGroup"
)

var reserved = map[string]bool{"title": true, "username": true, "password": true, "url": true, "notes": true, "otp": true}

// Field is a custom field of an entry.
type Field struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Protected bool   `json:"protected"`
}

// Attachment describes a file stored in an entry.
type Attachment struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Entry is one credential. Passwords, TOTP secrets and protected fields are in plain text here: only
// code that has just authenticated the user may hold one (the facade strips them from listings).
type Entry struct {
	ID          string       `json:"id"`
	GroupID     string       `json:"group_id"`
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	Username    string       `json:"username"`
	Password    string       `json:"password"`
	URL         string       `json:"url"`
	Notes       string       `json:"notes"`
	Tags        []string     `json:"tags"`
	Fields      []Field      `json:"fields"`
	TOTP        string       `json:"totp"` // an otpauth:// URI or a bare base32 secret
	ExpiresAt   int64        `json:"expires_at"`
	CreatedAt   int64        `json:"created_at"`
	ModifiedAt  int64        `json:"modified_at"`
	Attachments []Attachment `json:"attachments"`
	History     int          `json:"history"`
	InTrash     bool         `json:"in_trash"`
}

// Summary is what listings carry: no secrets, no notes.
type Summary struct {
	ID          string   `json:"id"`
	GroupID     string   `json:"group_id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Username    string   `json:"username"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
	HasPassword bool     `json:"has_password"`
	HasTOTP     bool     `json:"has_totp"`
	HasAttach   bool     `json:"has_attachments"`
	ModifiedAt  int64    `json:"modified_at"`
	ExpiresAt   int64    `json:"expires_at"`
	InTrash     bool     `json:"in_trash"`
}

// Group is a folder.
type Group struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"` // empty for the root
	Name     string `json:"name"`
	Path     string `json:"path"`
	Entries  int    `json:"entries"`
	IsTrash  bool   `json:"is_trash"`
}

// Version is one past state of an entry (secrets left out).
type Version struct {
	Index      int    `json:"index"`
	ModifiedAt int64  `json:"modified_at"`
	Title      string `json:"title"`
	Username   string `json:"username"`
	URL        string `json:"url"`
}

// Summarize derives the listing view.
func (e Entry) Summarize() Summary {
	return Summary{
		ID: e.ID, GroupID: e.GroupID, Kind: e.Kind, Title: e.Title, Username: e.Username, URL: e.URL,
		Tags: append([]string{}, e.Tags...), HasPassword: e.Password != "", HasTOTP: e.TOTP != "", HasAttach: len(e.Attachments) > 0,
		ModifiedAt: e.ModifiedAt, ExpiresAt: e.ExpiresAt, InTrash: e.InTrash,
	}
}

func validKind(k string) bool {
	switch k {
	case KindLogin, KindAPIKey, KindSecret, KindNote:
		return true
	}
	return false
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' }) {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func joinTags(tags []string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.TrimSpace(strings.NewReplacer(";", " ", ",", " ").Replace(t))
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ";")
}

// validate checks sizes and names of an entry that is about to be stored.
func (e *Entry) validate() error {
	if e.Kind == "" {
		e.Kind = KindLogin
	}
	if !validKind(e.Kind) {
		return invalid("unknown entry kind %q", e.Kind)
	}
	if strings.TrimSpace(e.Title) == "" {
		return invalid("an entry needs a title")
	}
	for _, s := range []string{e.Title, e.Username, e.Password, e.URL, e.Notes, e.TOTP} {
		if len(s) > MaxFieldBytes {
			return ErrTooLarge
		}
	}
	if len(e.Fields) > 100 || len(e.Tags) > 100 {
		return invalid("too many fields or tags")
	}
	seen := map[string]bool{}
	for _, f := range e.Fields {
		n := strings.TrimSpace(f.Name)
		if n == "" || len(n) > 128 || reserved[strings.ToLower(n)] || strings.HasPrefix(strings.ToLower(n), "kph:") {
			return invalid("invalid custom field name %q", f.Name)
		}
		if seen[strings.ToLower(n)] {
			return invalid("duplicate custom field %q", n)
		}
		seen[strings.ToLower(n)] = true
		if len(f.Value) > MaxFieldBytes {
			return ErrTooLarge
		}
	}
	return nil
}

// fromLib reads a library entry into the model. groupID and trash come from where it lives.
func fromLib(db *g.Database, le *g.Entry, groupID string, trash bool) Entry {
	e := Entry{
		ID: uuidHex(le.UUID), GroupID: groupID, Kind: KindLogin,
		Title: entryGet(le, fTitle), Username: entryGet(le, fUser), Password: entryGet(le, fPassword),
		URL: entryGet(le, fURL), Notes: entryGet(le, fNotes), TOTP: entryGet(le, fOTP),
		Tags: splitTags(le.Tags), Fields: []Field{}, Attachments: []Attachment{},
		CreatedAt: unix(le.Times.CreationTime), ModifiedAt: unix(le.Times.LastModificationTime), InTrash: trash,
	}
	if e.Tags == nil {
		e.Tags = []string{}
	}
	if le.Times.Expires.Bool {
		e.ExpiresAt = unix(le.Times.ExpiryTime)
	}
	for _, cd := range le.CustomData {
		if cd.Key == cdKind && validKind(cd.Value) {
			e.Kind = cd.Value
		}
	}
	for _, v := range le.Values {
		switch v.Key {
		case fTitle, fUser, fPassword, fURL, fNotes, fOTP:
		default:
			e.Fields = append(e.Fields, Field{Name: v.Key, Value: v.Value.Content, Protected: v.Value.Protected.Bool})
		}
	}
	for _, br := range le.Binaries {
		a := Attachment{Name: br.Name}
		if b := db.FindBinary(br.Value.ID); b != nil {
			if c, err := b.GetContentBytes(); err == nil {
				a.Size = int64(len(c))
			}
		}
		e.Attachments = append(e.Attachments, a)
	}
	for _, h := range le.Histories {
		e.History += len(h.Entries)
	}
	return e
}

func invalid(format string, a ...any) error { return wrapInvalid(format, a...) }
