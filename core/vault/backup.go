package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Backups are plain copies of the encrypted file as it was *before* a change, so any of them opens
// with the master credentials that were current when it was taken (docs/plan-vault.md §8).
const (
	keepNewest = 20
	keepDays   = 7
	keepWeeks  = 4

	snapshotMinGap = 10 * time.Second // routine saves do not snapshot more often than this
)

var backupNameRe = regexp.MustCompile(`^vault-(\d{8}T\d{6}Z)-([a-z0-9-]{1,24})\.kdbx$`)

// BackupInfo describes one restorable copy.
type BackupInfo struct {
	Name   string `json:"name"`
	Time   int64  `json:"time"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"` // save, delete, import, restore, rekey, merge, create, previous
}

func sanitizeReason(r string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(r) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			b.WriteRune(c)
		}
	}
	s := b.String()
	if s == "" {
		return "save"
	}
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}

// snapshotLocked stores old (the file about to be replaced) in the backup directory. Failures only
// cost the snapshot, never the save. v.mu held.
func (v *Vault) snapshotLocked(old []byte, reason string) {
	if len(old) == 0 {
		return
	}
	reason = sanitizeReason(reason)
	now := v.now().UTC()
	if reason == "save" && !v.lastSnapshot.IsZero() && now.Sub(v.lastSnapshot) < snapshotMinGap {
		return
	}
	dir := filepath.Join(v.dir, BackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	name := fmt.Sprintf("vault-%s-%s.kdbx", now.Format("20060102T150405Z"), reason)
	path := filepath.Join(dir, name)
	for i := 2; fileExists(path); i++ { // same second, same reason
		name = fmt.Sprintf("vault-%s-%s%d.kdbx", now.Format("20060102T150405Z"), reason, i)
		path = filepath.Join(dir, name)
	}
	if err := writeFileSync(path, old); err != nil {
		return
	}
	v.lastSnapshot = now
	pruneBackups(dir, now)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// BackupNow takes a snapshot of the vault file as it is, outside the routine ones (the "back up now"
// button). It works like every other snapshot: restorable with the credentials in force now.
func (v *Vault) BackupNow() (BackupInfo, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return BackupInfo{}, err
	}
	if err := v.saveLocked("manual-save"); err != nil { // makes sure the file on disk is current
		return BackupInfo{}, err
	}
	cur, err := os.ReadFile(v.path())
	if err != nil {
		return BackupInfo{}, fmt.Errorf("vault: read file: %w", err)
	}
	v.lastSnapshot = time.Time{}
	v.snapshotLocked(cur, "manual")
	for _, b := range ListBackups(v.dir) {
		if b.Reason == "manual" {
			return b, nil
		}
	}
	return BackupInfo{}, fmt.Errorf("vault: the snapshot could not be written")
}

// ListBackups lists the snapshots of the vault in dir, newest first, plus the previous generation of
// the main file. It works while the vault is locked.
func ListBackups(dir string) []BackupInfo {
	var out []BackupInfo
	entries, _ := os.ReadDir(filepath.Join(dir, BackupDir))
	for _, e := range entries {
		m := backupNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		t, err := time.Parse("20060102T150405Z", m[1])
		info, ierr := e.Info()
		if err != nil || ierr != nil {
			continue
		}
		out = append(out, BackupInfo{Name: e.Name(), Time: t.Unix(), Size: info.Size(), Reason: strings.TrimRight(m[2], "0123456789")})
	}
	if st, err := os.Stat(filepath.Join(dir, PrevName)); err == nil {
		out = append(out, BackupInfo{Name: PrevName, Time: st.ModTime().Unix(), Size: st.Size(), Reason: "previous"})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out
}

// pruneBackups applies the retention policy: the newest keepNewest, the newest of each of the last
// keepDays days and of each of the last keepWeeks weeks.
func pruneBackups(dir string, now time.Time) {
	entries, _ := os.ReadDir(dir)
	type item struct {
		name string
		t    time.Time
	}
	var items []item
	for _, e := range entries {
		if m := backupNameRe.FindStringSubmatch(e.Name()); m != nil {
			if t, err := time.Parse("20060102T150405Z", m[1]); err == nil {
				items = append(items, item{e.Name(), t})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].t.After(items[j].t) })
	keep := map[string]bool{}
	days, weeks := map[string]bool{}, map[string]bool{}
	for i, it := range items {
		if i < keepNewest {
			keep[it.name] = true
		}
		age := now.Sub(it.t)
		day := it.t.Format("2006-01-02")
		if age < time.Duration(keepDays)*24*time.Hour && !days[day] {
			days[day], keep[it.name] = true, true
		}
		y, wk := it.t.ISOWeek()
		week := fmt.Sprintf("%d-%d", y, wk)
		if age < time.Duration(keepWeeks)*7*24*time.Hour && !weeks[week] {
			weeks[week], keep[it.name] = true, true
		}
	}
	for _, it := range items {
		if !keep[it.name] {
			_ = os.Remove(filepath.Join(dir, it.name))
		}
	}
}

// backupPath resolves a backup name to a file, refusing anything that is not one of ours.
func backupPath(dir, name string) (string, error) {
	if name == PrevName {
		return filepath.Join(dir, PrevName), nil
	}
	if !backupNameRe.MatchString(name) {
		return "", invalid("not a backup name")
	}
	return filepath.Join(dir, BackupDir, name), nil
}

// BackupCheck is the result of opening a backup.
type BackupCheck struct {
	Entries    int   `json:"entries"`
	Groups     int   `json:"groups"`
	InTrash    int   `json:"in_trash"`
	ModifiedAt int64 `json:"modified_at"` // newest entry change inside it
}

// VerifyBackup opens a backup with c and reports what is in it: the "can this really be restored"
// button. Backups made before a master password change need the old password.
func VerifyBackup(dir, name string, c Creds) (BackupCheck, error) {
	p, err := backupPath(dir, name)
	if err != nil {
		return BackupCheck{}, err
	}
	data, err := readFileLimited(p)
	if err != nil {
		return BackupCheck{}, err
	}
	db, err := decode(data, c)
	if err != nil {
		return BackupCheck{}, err
	}
	var chk BackupCheck
	walk(db, func(le *libEntry, groupID string, trash bool) {
		chk.Entries++
		if trash {
			chk.InTrash++
		}
		if m := unix(le.Times.LastModificationTime); m > chk.ModifiedAt {
			chk.ModifiedAt = m
		}
	}, func(*libGroup, string, bool) { chk.Groups++ })
	return chk, nil
}
