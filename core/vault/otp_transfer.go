package vault

import (
	"encoding/base32"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

var (
	ErrOTPIncomplete  = errors.New("the authenticator migration is missing QR codes")
	ErrOTPUnsupported = errors.New("only time-based OTP (TOTP) is supported")
)

type OTPImportResult struct {
	Added       int `json:"added"`
	Duplicates  int `json:"duplicates"`
	Unsupported int `json:"unsupported"`
}

type OTPExportEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	Link     string `json:"link"`
}

type otpAccount struct {
	title, username string
	spec            TOTPSpec
}

func (t TOTPSpec) otpLink(issuer, account string) string {
	label := account
	if issuer != "" {
		label = issuer
		if account != "" {
			label += ":" + account
		}
	}
	query := url.Values{
		"secret":    {base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(t.Secret)},
		"digits":    {strconv.Itoa(t.Digits)},
		"period":    {strconv.Itoa(t.Period)},
		"algorithm": {t.Algorithm},
	}
	if issuer != "" {
		query.Set("issuer", issuer)
	}
	return "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode()
}

func otpKey(t TOTPSpec) string { return t.otpLink("", "") }

func otpURIAccount(u *url.URL) (otpAccount, error) {
	if strings.EqualFold(u.Host, "hotp") {
		return otpAccount{}, ErrOTPUnsupported
	}
	query, err := url.ParseQuery(u.RawQuery)
	label := strings.TrimPrefix(u.Path, "/")
	if err != nil || !utf8.ValidString(label) || !utf8.ValidString(query.Get("issuer")) ||
		u.User != nil || u.Fragment != "" || !strings.EqualFold(u.Host, "totp") {
		return otpAccount{}, invalid("invalid OTP link")
	}
	spec, err := ParseTOTP(u.String())
	if err != nil {
		return otpAccount{}, invalid("invalid OTP link or TOTP parameters")
	}
	title, account := otpNames(label, query.Get("issuer"))
	return otpAccount{title, account, spec}, nil
}

func otpNames(label, issuer string) (string, string) {
	label, issuer = strings.TrimSpace(label), strings.TrimSpace(issuer)
	if a, b, ok := strings.Cut(label, ":"); ok && (issuer == "" || strings.EqualFold(strings.TrimSpace(a), issuer)) {
		if issuer == "" {
			issuer = strings.TrimSpace(a)
		}
		label = strings.TrimSpace(b)
	}
	if issuer == "" {
		issuer = label
	}
	if issuer == "" {
		issuer = "OTP"
	}
	return issuer, label
}

type otpMigration struct {
	accounts              []otpAccount
	version, total, index int
	id                    int32
	unsupported           int
}

func parseOTPMigration(data []byte) (otpMigration, error) {
	m := otpMigration{total: 1}
	for len(data) > 0 {
		field, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return m, invalid("invalid authenticator migration")
		}
		data = data[n:]
		switch {
		case field == 1 && typ == protowire.BytesType:
			entry, size := protowire.ConsumeBytes(data)
			n = size
			if n >= 0 {
				account, err := parseMigrationAccount(entry)
				if errors.Is(err, ErrOTPUnsupported) {
					m.unsupported++
				} else if err != nil {
					return m, err
				} else {
					m.accounts = append(m.accounts, account)
				}
			}
		case field >= 2 && field <= 5 && typ == protowire.VarintType:
			value, size := protowire.ConsumeVarint(data)
			n = size
			switch field {
			case 2:
				m.version = int(value)
			case 3:
				m.total = int(value)
			case 4:
				m.index = int(value)
			case 5:
				m.id = int32(value)
			}
		default:
			n = protowire.ConsumeFieldValue(field, typ, data)
		}
		if n < 0 {
			return m, invalid("invalid authenticator migration")
		}
		data = data[n:]
	}
	if m.version != 1 || m.total < 1 || m.total > 256 || m.index < 0 || m.index >= m.total ||
		len(m.accounts)+m.unsupported == 0 {
		return m, invalid("invalid authenticator migration version or batch")
	}
	return m, nil
}

func parseMigrationAccount(data []byte) (otpAccount, error) {
	var secret []byte
	var name, issuer string
	var algorithm, digits, kind uint64
	for len(data) > 0 {
		field, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return otpAccount{}, invalid("invalid authenticator migration entry")
		}
		data = data[n:]
		switch {
		case field >= 1 && field <= 3 && typ == protowire.BytesType:
			value, size := protowire.ConsumeBytes(data)
			n = size
			switch field {
			case 1:
				secret = value
			case 2:
				name = string(value)
			case 3:
				issuer = string(value)
			}
		case field >= 4 && field <= 6 && typ == protowire.VarintType:
			value, size := protowire.ConsumeVarint(data)
			n = size
			switch field {
			case 4:
				algorithm = value
			case 5:
				digits = value
			case 6:
				kind = value
			}
		default:
			n = protowire.ConsumeFieldValue(field, typ, data)
		}
		if n < 0 {
			return otpAccount{}, invalid("invalid authenticator migration entry")
		}
		data = data[n:]
	}
	algo, algoOK := map[uint64]string{0: "SHA1", 1: "SHA1", 2: "SHA256", 3: "SHA512"}[algorithm]
	d, digitsOK := map[uint64]int{0: 6, 1: 6, 2: 8}[digits]
	if kind != 2 || !algoOK || !digitsOK {
		return otpAccount{}, ErrOTPUnsupported
	}
	if len(secret) < 10 || len(secret) > 1024 || !utf8.ValidString(name) || !utf8.ValidString(issuer) {
		return otpAccount{}, invalid("invalid authenticator migration entry")
	}
	title, username := otpNames(name, issuer)
	return otpAccount{title, username, TOTPSpec{Secret: secret, Digits: d, Period: 30, Algorithm: algo}}, nil
}

func parseOTPImport(payloads []string) ([]otpAccount, OTPImportResult, error) {
	var accounts []otpAccount
	var result OTPImportResult
	type batchKey struct {
		id    int32
		total int
	}
	type batchPart struct {
		key   batchKey
		index int
	}
	batches := map[batchKey]int{}
	parts := map[batchPart]string{}
	bytesRead := 0
	if len(payloads) == 0 || len(payloads) > 10000 {
		return nil, result, invalid("expected 1 to 10000 OTP links")
	}
	for _, raw := range payloads {
		bytesRead += len(raw)
		if bytesRead > 4<<20 {
			return nil, result, ErrTooLarge
		}
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, result, invalid("invalid OTP link")
		}
		switch strings.ToLower(u.Scheme) {
		case "otpauth":
			a, err := otpURIAccount(u)
			if errors.Is(err, ErrOTPUnsupported) {
				result.Unsupported++
				continue
			}
			if err != nil {
				return nil, result, err
			}
			accounts = append(accounts, a)
		case "otpauth-migration":
			q, err := url.ParseQuery(u.RawQuery)
			if err != nil || !strings.EqualFold(u.Host, "offline") || u.User != nil || u.Fragment != "" {
				return nil, result, invalid("invalid authenticator migration link")
			}
			encoded := strings.ReplaceAll(q.Get("data"), " ", "+")
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				data, err = base64.RawStdEncoding.DecodeString(encoded)
			}
			if err != nil {
				data, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
			}
			if err != nil {
				return nil, result, invalid("invalid authenticator migration data")
			}
			m, err := parseOTPMigration(data)
			if err != nil {
				return nil, result, err
			}
			if m.total > 1 {
				key := batchKey{m.id, m.total}
				part := batchPart{key, m.index}
				if old, ok := parts[part]; ok {
					if old != string(data) {
						return nil, result, invalid("conflicting authenticator migration QR codes")
					}
					continue
				}
				parts[part] = string(data)
				batches[key]++
			}
			accounts = append(accounts, m.accounts...)
			result.Unsupported += m.unsupported
		default:
			return nil, result, invalid("expected an otpauth or authenticator migration link")
		}
	}
	for key, count := range batches {
		if key.total != count {
			return nil, result, ErrOTPIncomplete
		}
	}
	return accounts, result, nil
}

// ImportOTP only adds login entries; importing a second time never overwrites a saved password.
func (v *Vault) ImportOTP(payloads []string, groupID string) (OTPImportResult, error) {
	accounts, result, err := parseOTPImport(payloads)
	if err != nil {
		return OTPImportResult{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return OTPImportResult{}, err
	}
	if groupID != "" {
		group, _, trash := v.findGroup(groupID)
		if group == nil || trash {
			return OTPImportResult{}, ErrNotFound
		}
	}
	existing := map[string]bool{}
	walk(v.db, func(le *libEntry, _ string, trash bool) {
		if trash || entryGet(le, fOTP) == "" {
			return
		}
		if t, err := ParseTOTP(entryGet(le, fOTP)); err == nil {
			existing[otpKey(t)] = true
		}
	}, nil)
	var entries []Entry
	for _, a := range accounts {
		key := otpKey(a.spec)
		if existing[key] {
			result.Duplicates++
			continue
		}
		existing[key] = true
		e := Entry{Kind: KindLogin, Title: a.title, Username: a.username, TOTP: a.spec.otpLink(a.title, a.username)}
		if err := e.validate(); err != nil {
			return OTPImportResult{}, invalid("invalid OTP account name or parameters")
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return result, nil
	}
	if len(entries) > MaxEntries-v.countEntries() {
		return OTPImportResult{}, ErrTooLarge
	}
	oldGroups := v.db.Content.Root.Groups
	v.db.Content.Root.Groups = copyImportGroups(oldGroups)
	gr := v.root()
	if groupID != "" {
		gr, _, _ = v.findGroup(groupID)
	}
	now := v.nowWrap()
	for _, e := range entries {
		le := newEntry()
		v.applyEntry(&le, e)
		le.Times.CreationTime, le.Times.LastModificationTime, le.Times.LastAccessTime, le.Times.LocationChanged = &now, &now, &now, &now
		gr.Entries = append(gr.Entries, le)
	}
	if err := v.commit("import"); err != nil {
		v.db.Content.Root.Groups = oldGroups
		return OTPImportResult{}, err
	}
	result.Added = len(entries)
	return result, nil
}

// ExportOTP omits deleted entries and preserves the parameters of every valid TOTP entry.
func (v *Vault) ExportOTP(entryID string) ([]OTPExportEntry, int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, 0, err
	}
	out := []OTPExportEntry{}
	skipped := 0
	walk(v.db, func(le *libEntry, _ string, trash bool) {
		id := uuidHex(le.UUID)
		if trash || (entryID != "" && id != entryID) {
			return
		}
		raw := entryGet(le, fOTP)
		if raw == "" {
			return
		}
		t, err := ParseTOTP(raw)
		if err != nil {
			skipped++
			return
		}
		title, user := entryGet(le, fTitle), entryGet(le, fUser)
		if u, err := url.Parse(raw); err == nil && strings.EqualFold(u.Scheme, "otpauth") && u.Path != "" {
			if a, err := otpURIAccount(u); err == nil {
				title, user = a.title, a.username
			}
		}
		out = append(out, OTPExportEntry{ID: id, Title: title, Username: user, Link: t.otpLink(title, user)})
	}, nil)
	return out, skipped, nil
}
