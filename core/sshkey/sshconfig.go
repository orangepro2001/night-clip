package sshkey

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigHost describes a Host declaration, before OpenSSH applies defaults
// or evaluates conditions. Wildcards and negations are kept out of Aliases.
type ConfigHost struct {
	Aliases       []string `json:"aliases"`
	HostName      string   `json:"host_name,omitempty"`
	User          string   `json:"user,omitempty"`
	Port          string   `json:"port,omitempty"`
	IdentityFiles []string `json:"identity_files"`
}

type configScan struct {
	hosts      []ConfigHost
	identities map[string][]string
	remaining  int
	bytesLeft  int64
	partial    bool
}

func (c *configScan) read(path, dir, home string, depth int, patterns []string, host int) {
	if depth > 8 || c.remaining == 0 || !filepath.IsLocal(filepath.Base(path)) {
		c.partial = true
		return
	}
	c.remaining--
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() > c.bytesLeft {
		c.partial = true
		return
	}
	data, big, err := readCapped(path, c.bytesLeft)
	if err != nil || big {
		c.partial = true
		return
	}
	c.bytesLeft -= int64(len(data))
	c.parse(string(data), dir, home, depth, patterns, host)
}

func (c *configScan) parse(text, dir, home string, depth int, patterns []string, host int) {
	sc := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitConfigLine(line)
		key = strings.ToLower(key)
		switch key {
		case "host", "match", "include", "hostname", "user", "port", "identityfile":
		default:
			continue
		}
		args := configArgs(val)
		switch key {
		case "host":
			patterns, host = args, -1
			var aliases []string
			for _, name := range args {
				if name != "" && !strings.HasPrefix(name, "!") && !strings.ContainsAny(name, "*?") && !contains(aliases, name) {
					aliases = append(aliases, name)
				}
			}
			if len(aliases) > 0 {
				host = len(c.hosts)
				c.hosts = append(c.hosts, ConfigHost{Aliases: aliases, IdentityFiles: []string{}})
			}
		case "match":
			patterns, host = []string{"Match " + val}, -1
		case "include":
			for _, arg := range args {
				if c.remaining == 0 || depth == 8 {
					c.partial = true
					break
				}
				p := expandConfigPath(arg, home)
				if p == "" {
					continue
				}
				if !filepath.IsAbs(p) {
					p = filepath.Join(dir, p)
				}
				matches, err := filepath.Glob(p)
				if err != nil {
					c.partial = true
				}
				for _, match := range matches {
					if c.remaining == 0 {
						c.partial = true
						break
					}
					c.read(match, dir, home, depth+1, patterns, host)
				}
			}
		case "hostname", "user", "port":
			if host < 0 || len(args) != 1 {
				continue
			}
			h := &c.hosts[host]
			field := &h.HostName
			switch key {
			case "user":
				field = &h.User
			case "port":
				field = &h.Port
			}
			if *field == "" {
				*field = args[0]
			}
		case "identityfile":
			if len(args) != 1 {
				continue
			}
			if host >= 0 && !contains(c.hosts[host].IdentityFiles, args[0]) {
				c.hosts[host].IdentityFiles = append(c.hosts[host].IdentityFiles, args[0])
			}
			p := expandConfigPath(args[0], home)
			if p == "" {
				continue
			}
			k := pathKey(p)
			for _, h := range patterns {
				if !contains(c.identities[k], h) {
					c.identities[k] = append(c.identities[k], h)
				}
			}
		}
	}
	c.partial = c.partial || sc.Err() != nil
}

func splitConfigLine(line string) (string, string) {
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, ""
	}
	key := line[:i]
	rest := strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimPrefix(rest, "=")
	return key, strings.TrimSpace(rest)
}

// configArgs reads quoted arguments and comments while preserving literal
// Windows path separators.
func configArgs(s string) []string {
	var args []string
	for s = strings.TrimLeft(s, " \t"); s != "" && s[0] != '#'; s = strings.TrimLeft(s, " \t") {
		if s[0] == '"' || s[0] == '\'' {
			end := strings.IndexByte(s[1:], s[0])
			if end < 0 {
				return nil
			}
			args = append(args, s[1:end+1])
			s = s[end+2:]
			continue
		}
		end := strings.IndexAny(s, " \t")
		if end < 0 {
			return append(args, s)
		}
		args = append(args, s[:end])
		s = s[end:]
	}
	return args
}

// expandConfigPath applies the expansions ssh does for IdentityFile that
// matter for local paths: a leading ~, %d (home) and %%.
func expandConfigPath(p, home string) string {
	if p == "" || strings.EqualFold(p, "none") {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = home + p[1:]
	}
	p = strings.ReplaceAll(p, "%d", home)
	p = strings.ReplaceAll(p, "%%", "%")
	return filepath.Clean(filepath.FromSlash(p))
}

// pathKey normalises a path for comparison (case-insensitive on Windows).
func pathKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
