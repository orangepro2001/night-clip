package sshkey

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
)

// SettingsAttachment is the attachment name KeeAgent and KeePassXC use for
// per-entry SSH agent settings.
const SettingsAttachment = "KeeAgent.settings"

// AgentSettings is the part of KeeAgent.settings this program reads.
type AgentSettings struct {
	AllowUseOfSshKey                bool
	AddAtDatabaseOpen               bool
	RemoveAtDatabaseClose           bool
	UseConfirmConstraintWhenAdding  bool
	UseLifetimeConstraintWhenAdding bool
	LifetimeConstraintDuration      int
	LocationType                    string // "attachment" or "file"
	AttachmentName                  string
	FileName                        string
}

// Enabled reports whether the entry asks for its key to be offered by an
// agent while the database is open.
func (s AgentSettings) Enabled() bool { return s.AllowUseOfSshKey && s.AddAtDatabaseOpen }

type xmlSettings struct {
	XMLName                         xml.Name `xml:"EntrySettings"`
	AllowUseOfSshKey                bool
	AddAtDatabaseOpen               bool
	RemoveAtDatabaseClose           bool
	UseConfirmConstraintWhenAdding  bool
	UseLifetimeConstraintWhenAdding bool
	LifetimeConstraintDuration      int
	Location                        struct {
		SelectedType   string
		AttachmentName string
		FileName       string
	}
}

// ParseAgentSettings reads KeeAgent.settings in UTF-8 or UTF-16 (KeeAgent
// and KeePassXC 2.7 write UTF-16LE with a byte-order mark).
func ParseAgentSettings(b []byte) (AgentSettings, error) {
	if len(b) > 64<<10 {
		return AgentSettings{}, corrupt("KeeAgent.settings is too large")
	}
	text, _ := decodeXMLText(b)
	d := xml.NewDecoder(strings.NewReader(text))
	// The text is already UTF-8 whatever the declaration says.
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var x xmlSettings
	if err := d.Decode(&x); err != nil {
		return AgentSettings{}, corrupt("KeeAgent.settings: %v", err)
	}
	return AgentSettings{
		AllowUseOfSshKey:                x.AllowUseOfSshKey,
		AddAtDatabaseOpen:               x.AddAtDatabaseOpen,
		RemoveAtDatabaseClose:           x.RemoveAtDatabaseClose,
		UseConfirmConstraintWhenAdding:  x.UseConfirmConstraintWhenAdding,
		UseLifetimeConstraintWhenAdding: x.UseLifetimeConstraintWhenAdding,
		LifetimeConstraintDuration:      x.LifetimeConstraintDuration,
		LocationType:                    strings.ToLower(strings.TrimSpace(x.Location.SelectedType)),
		AttachmentName:                  strings.TrimSpace(x.Location.AttachmentName),
		FileName:                        strings.TrimSpace(x.Location.FileName),
	}, nil
}

// NewAgentSettings writes a KeeAgent.settings that points at a key
// attachment, in the UTF-16LE form KeeAgent itself produces.
func NewAgentSettings(attachment string, enabled bool) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\r\n")
	b.WriteString(`<EntrySettings xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` + "\r\n")
	b.WriteString("  <AllowUseOfSshKey>true</AllowUseOfSshKey>\r\n")
	b.WriteString("  <AddAtDatabaseOpen>" + xmlBool(enabled) + "</AddAtDatabaseOpen>\r\n")
	b.WriteString("  <RemoveAtDatabaseClose>true</RemoveAtDatabaseClose>\r\n")
	b.WriteString("  <UseConfirmConstraintWhenAdding>false</UseConfirmConstraintWhenAdding>\r\n")
	b.WriteString("  <UseLifetimeConstraintWhenAdding>false</UseLifetimeConstraintWhenAdding>\r\n")
	b.WriteString("  <LifetimeConstraintDuration>600</LifetimeConstraintDuration>\r\n")
	b.WriteString("  <Location>\r\n")
	b.WriteString("    <SelectedType>attachment</SelectedType>\r\n")
	b.WriteString("    <AttachmentName>" + xmlEscape(attachment) + "</AttachmentName>\r\n")
	b.WriteString("    <SaveAttachmentToTempFile>false</SaveAttachmentToTempFile>\r\n")
	b.WriteString("    <FileName />\r\n")
	b.WriteString("  </Location>\r\n")
	b.WriteString("</EntrySettings>")
	return encodeXMLText(b.String(), true)
}

var settingsOpenTag = regexp.MustCompile(`<EntrySettings\b[^>]*>`)

// SetAgentEnabled rewrites AllowUseOfSshKey and AddAtDatabaseOpen and leaves
// every other byte of the document as it was, so elements written by newer
// KeePassXC versions survive.
func SetAgentEnabled(b []byte, enabled bool) ([]byte, error) {
	if _, err := ParseAgentSettings(b); err != nil {
		return nil, err
	}
	text, utf16 := decodeXMLText(b)
	if enabled {
		text = setElement(text, "AllowUseOfSshKey", "true")
	}
	text = setElement(text, "AddAtDatabaseOpen", xmlBool(enabled))
	out := encodeXMLText(text, utf16)
	s, err := ParseAgentSettings(out)
	if err != nil {
		return nil, err
	}
	if s.AddAtDatabaseOpen != enabled || (enabled && !s.AllowUseOfSshKey) {
		return nil, corrupt("KeeAgent.settings could not be updated")
	}
	return out, nil
}

var settingsSelfClosed = regexp.MustCompile(`<EntrySettings\b([^>]*?)\s*/>`)

func setElement(text, name, value string) string {
	re := regexp.MustCompile(`<` + name + `(?:\s[^>]*)?>[^<]*</` + name + `\s*>|<` + name + `(?:\s[^>]*)?/>`)
	repl := "<" + name + ">" + value + "</" + name + ">"
	if re.MatchString(text) {
		return re.ReplaceAllLiteralString(text, repl)
	}
	text = settingsSelfClosed.ReplaceAllString(text, "<EntrySettings$1></EntrySettings>")
	loc := settingsOpenTag.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return text[:loc[1]] + "\r\n  " + repl + text[loc[1]:]
}

// decodeXMLText returns UTF-8 text and whether the input was UTF-16.
func decodeXMLText(b []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return string(decodeUTF16(b[2:], false)), true
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return string(decodeUTF16(b[2:], true)), true
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:]), false
	case len(b) >= 4 && b[0] == '<' && b[1] == 0:
		return string(decodeUTF16(b, false)), true
	}
	return string(b), false
}

func encodeXMLText(s string, asUTF16 bool) []byte {
	if !asUTF16 {
		return []byte(s)
	}
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2, 2+2*len(u))
	out[0], out[1] = 0xFF, 0xFE
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func xmlBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
