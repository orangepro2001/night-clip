package sshagent

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// KnownHosts answers "which host names belong to this host key" from a
// known_hosts file, for display. Hashed entries (HashKnownHosts yes) cannot
// be reversed and are skipped. The file is re-read when it changes.
type KnownHosts struct {
	path string

	mu    sync.Mutex
	mod   time.Time
	size  int64
	byKey map[string][]string
}

const maxKnownHosts = 4 << 20

// NewKnownHosts reads path lazily.
func NewKnownHosts(path string) *KnownHosts { return &KnownHosts{path: path} }

// Names returns the plain host names recorded for hk, possibly none.
func (k *KnownHosts) Names(hk ssh.PublicKey) []string {
	if k == nil || hk == nil || k.path == "" {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	st, err := os.Stat(k.path)
	if err != nil || st.Size() > maxKnownHosts {
		return nil
	}
	if k.byKey == nil || !st.ModTime().Equal(k.mod) || st.Size() != k.size {
		data, err := os.ReadFile(k.path)
		if err != nil {
			return nil
		}
		k.byKey, k.mod, k.size = parseKnownHosts(data), st.ModTime(), st.Size()
	}
	return append([]string(nil), k.byKey[string(hk.Marshal())]...)
}

func parseKnownHosts(data []byte) map[string][]string {
	out := map[string][]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == '@' {
			continue
		}
		hosts, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(rest)))
		if err != nil {
			continue
		}
		key := string(pub.Marshal())
		for _, h := range strings.Split(hosts, ",") {
			if h == "" || strings.HasPrefix(h, "|") || strings.HasPrefix(h, "!") || strings.ContainsAny(h, "*?") {
				continue
			}
			if !contains(out[key], h) {
				out[key] = append(out[key], h)
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
