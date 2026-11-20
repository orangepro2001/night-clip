package sshkey

import (
	"bufio"
	"path/filepath"
	"runtime"
	"strings"
)

// configIdentities maps each IdentityFile in an ssh_config to the Host
// patterns it appears under. Keys are pathKey() values. Lines before the
// first Host or Match belong to "*". Include directives are not followed.
func configIdentities(text, home string) map[string][]string {
	out := map[string][]string{}
	current := []string{"*"}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitConfigLine(line)
		switch strings.ToLower(key) {
		case "host":
			current = strings.Fields(val)
		case "match":
			current = []string{"Match " + val}
		case "identityfile":
			p := expandConfigPath(unquote(val), home)
			if p == "" {
				continue
			}
			k := pathKey(p)
			for _, h := range current {
				if !contains(out[k], h) {
					out[k] = append(out[k], h)
				}
			}
		}
	}
	return out
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

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
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
