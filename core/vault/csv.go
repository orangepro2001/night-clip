package vault

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// CSV export columns, in order. The file holds every secret in plain text: callers must make the user
// confirm and must say so.
var csvHeader = []string{"Group", "Title", "Username", "Password", "URL", "Notes", "TOTP", "Tags", "Kind"}

// csvSafe stops a spreadsheet from running a cell: a value starting with = + - @ tab or CR gets a
// leading apostrophe. ImportCSV removes exactly that apostrophe again.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func csvUnsafe(s string) string {
	if len(s) > 1 && s[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(s[1])) {
		return s[1:]
	}
	return s
}

// ExportCSV writes the vault (without the recycle bin) as CSV.
func (v *Vault) ExportCSV(out io.Writer) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	w := csv.NewWriter(out)
	if err := w.Write(csvHeader); err != nil {
		return err
	}
	paths := map[string]string{}
	var err error
	walk(v.db, nil, func(gr *libGroup, parent string, _ bool) {
		id := uuidHex(gr.UUID)
		if parent == "" {
			paths[id] = ""
		} else if paths[parent] == "" {
			paths[id] = gr.Name
		} else {
			paths[id] = paths[parent] + "/" + gr.Name
		}
	})
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		if trash || err != nil {
			return
		}
		e := fromLib(v.db, le, gid, false)
		// Text columns that other devices or imports could have filled get a leading quote when a
		// spreadsheet would read them as a formula; the password and TOTP columns are written as they
		// are, they are secrets the user will paste, not open in Excel.
		err = w.Write([]string{csvSafe(paths[gid]), csvSafe(e.Title), csvSafe(e.Username), e.Password, csvSafe(e.URL), csvSafe(e.Notes), e.TOTP, csvSafe(strings.Join(e.Tags, ";")), e.Kind})
	}, nil)
	if err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

// header aliases of the common exporters (Chrome/Edge, Firefox, Bitwarden, 1Password, LastPass, KeePassXC)
var csvAliases = map[string][]string{
	"group":    {"group", "folder", "grouping", "login_folder", "collection", "vault"},
	"title":    {"title", "name", "login_name", "account"},
	"username": {"username", "user", "login", "login_username", "login name", "email", "user name"},
	"password": {"password", "login_password", "pass"},
	"url":      {"url", "login_uri", "website", "web site", "login_url", "origin", "hostname"},
	"notes":    {"notes", "note", "extra", "comments", "comment", "secure note"},
	"totp":     {"totp", "login_totp", "otpauth", "otp", "one-time password", "totp secret"},
	"tags":     {"tags", "tag"},
	"kind":     {"kind", "type"},
}

// ImportCSV adds entries from a CSV with a header row; the columns are recognised by their usual
// names. Groups are created from the group/folder column ("A/B" makes nested groups). Rows without a
// title and a secret are skipped. It returns how many entries were added.
func (v *Vault) ImportCSV(in io.Reader) (added, skipped int, err error) {
	r := csv.NewReader(io.LimitReader(in, MaxFileBytes))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	head, err := r.Read()
	if err != nil {
		return 0, 0, invalid("the CSV has no header row")
	}
	col := map[string]int{}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		for key, names := range csvAliases {
			for _, n := range names {
				if h == n {
					if _, taken := col[key]; !taken {
						col[key] = i
					}
				}
			}
		}
	}
	if _, ok := col["title"]; !ok {
		if _, ok2 := col["url"]; !ok2 {
			return 0, 0, invalid("the CSV needs a title (or name) column")
		}
	}
	if _, ok := col["password"]; !ok {
		return 0, 0, invalid("the CSV needs a password column")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return 0, 0, err
	}
	get := func(rec []string, key string) string {
		if i, ok := col[key]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	groups := map[string]string{}
	groupCount := v.countGroups()
	groupFor := func(path string) string {
		if path == "" {
			return ""
		}
		if id, ok := groups[path]; ok {
			return id
		}
		parent := ""
		var cur []string
		for _, part := range strings.Split(path, "/") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if len(cur) >= maxGroupDepth-1 || groupCount >= maxGroups { // too deep or too many: file the rest in the last group
				break
			}
			cur = append(cur, part)
			key := strings.Join(cur, "/")
			if id, ok := groups[key]; ok {
				parent = id
				continue
			}
			pg := v.root()
			if parent != "" {
				pg, _, _ = v.findGroup(parent)
			}
			// reuse an existing group of that name
			found := ""
			for i := range pg.Groups {
				if pg.Groups[i].Name == part && uuidHex(pg.Groups[i].UUID) != binID(v.db) {
					found = uuidHex(pg.Groups[i].UUID)
				}
			}
			if found == "" {
				ng := v.newGroupNamed(part)
				pg.Groups = append(pg.Groups, ng)
				groupCount++
				found = uuidHex(ng.UUID)
			}
			groups[key] = found
			parent = found
		}
		return parent
	}
	for {
		rec, rerr := r.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if added > 0 {
				_ = v.commit("import")
			}
			return added, skipped, fmt.Errorf("%w: CSV line: %v", ErrInvalid, rerr)
		}
		e := Entry{
			Title: csvUnsafe(get(rec, "title")), Username: csvUnsafe(get(rec, "username")), Password: get(rec, "password"),
			URL: csvUnsafe(get(rec, "url")), Notes: csvUnsafe(get(rec, "notes")), TOTP: get(rec, "totp"), Kind: get(rec, "kind"),
		}
		if e.Title == "" {
			e.Title = e.URL
		}
		if !validKind(e.Kind) {
			e.Kind = KindLogin
		}
		if t := get(rec, "tags"); t != "" {
			e.Tags = splitTags(t)
		}
		if e.TOTP != "" {
			if _, perr := ParseTOTP(e.TOTP); perr != nil {
				e.Notes = strings.TrimSpace(e.Notes + "\nTOTP (not valid, kept as text): " + e.TOTP)
				e.TOTP = ""
			}
		}
		if e.Title == "" || e.Password == "" && e.Notes == "" && e.Username == "" || e.validate() != nil || v.countEntries() >= MaxEntries {
			skipped++
			continue
		}
		le := newEntry()
		v.applyEntry(&le, e)
		now := v.nowWrap()
		le.Times.CreationTime, le.Times.LastModificationTime, le.Times.LastAccessTime, le.Times.LocationChanged = &now, &now, &now, &now
		gr := v.root()
		if gid := groupFor(get(rec, "group")); gid != "" {
			gr, _, _ = v.findGroup(gid)
		}
		gr.Entries = append(gr.Entries, le)
		added++
	}
	if added == 0 {
		return 0, skipped, nil
	}
	return added, skipped, v.commit("import")
}
