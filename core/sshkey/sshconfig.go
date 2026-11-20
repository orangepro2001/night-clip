package sshkey

import (
	"bufio"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
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
	rules      []configRule
}

type configScope struct {
	patterns []string
	outer    *configScope
}

type configRule struct {
	key, value string
	scope      *configScope
}

// ConfigTarget is a named Host and its literal known_hosts lookup identity.
// An empty KnownHost means the config cannot be resolved statically.
type ConfigTarget struct {
	Alias     string `json:"alias"`
	HostName  string `json:"host_name,omitempty"`
	Port      int    `json:"port,omitempty"`
	KnownHost string `json:"known_host,omitempty"`
}

// ConfigTargets reads host definitions without opening private key files or
// invoking OpenSSH. includeDir is the base for relative Include expressions.
func ConfigTargets(file, includeDir, home string) ([]ConfigTarget, bool) {
	cfg := newConfigScan()
	cfg.read(file, includeDir, home, 0, []string{"*"}, -1, nil)
	return cfg.targets(), cfg.partial
}

func newConfigScan() configScan {
	return configScan{hosts: []ConfigHost{}, identities: map[string][]string{}, remaining: 64, bytesLeft: 1 << 20}
}

func (c *configScan) read(path, dir, home string, depth int, patterns []string, host int, outer *configScope) {
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
	c.parse(string(data), dir, home, depth, patterns, host, outer)
}

func (c *configScan) parse(text, dir, home string, depth int, patterns []string, host int, outer *configScope) {
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
		case "host", "match", "include", "hostname", "user", "port", "identityfile", "hostkeyalias", "canonicalizehostname":
		default:
			continue
		}
		args := configArgs(val)
		switch key {
		case "hostname", "port", "hostkeyalias", "canonicalizehostname":
			if len(args) == 1 {
				if len(c.rules) < 4096 {
					c.rules = append(c.rules, configRule{key, args[0], &configScope{patterns, outer}})
				} else {
					c.partial = true
				}
			}
		}
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
			if len(args) == 1 && strings.EqualFold(args[0], "all") {
				patterns = []string{"*"}
			}
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
					c.read(match, dir, home, depth+1, patterns, host, &configScope{patterns, outer})
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

func (c *configScan) targets() []ConfigTarget {
	targets := []ConfigTarget{}
	seen := map[string]bool{}
	for _, entry := range c.hosts {
		for _, alias := range entry.Aliases {
			name := strings.ToLower(alias)
			if !seen[name] {
				if len(targets) == 512 {
					c.partial = true
					return targets
				}
				seen[name] = true
				targets = append(targets, c.target(alias))
			}
		}
	}
	return targets
}

func (c *configScan) target(alias string) ConfigTarget {
	out := ConfigTarget{Alias: alias}
	if c.partial {
		return out
	}
	values := map[string]string{}
	for _, r := range c.rules {
		if _, seen := values[r.key]; seen {
			continue
		}
		match, known := r.scope.matches(alias)
		if !known {
			// A Match clause may supply the first value. Do not guess which
			// server this alias will identify.
			values[r.key] = ""
		} else if match {
			values[r.key] = r.value
		}
	}
	get := func(key, fallback string) string {
		if value, ok := values[key]; ok {
			return value
		}
		return fallback
	}
	host := get("hostname", alias)
	host = strings.NewReplacer("%%", "%", "%h", alias).Replace(host)
	host = strings.ToLower(host)
	port, err := strconv.Atoi(get("port", "22"))
	if host == "" || strings.ContainsAny(host, "%*?[]") || err != nil || port < 1 || port > 65535 {
		return out
	}
	out.HostName, out.Port = host, port
	if keyAlias, set := values["hostkeyalias"]; set {
		out.KnownHost = keyAlias
	} else if strings.EqualFold(get("canonicalizehostname", "no"), "no") {
		out.KnownHost = host
		if port != 22 {
			out.KnownHost = "[" + host + "]:" + strconv.Itoa(port)
		}
	}
	return out
}

// matches honours Host's positive and negated patterns in each enclosing
// Include. Match predicates are left to OpenSSH.
func (s *configScope) matches(alias string) (match, known bool) {
	known = true
	name := strings.ToLower(alias)
	for scope := s; scope != nil; scope = scope.outer {
		if len(scope.patterns) == 1 && strings.HasPrefix(scope.patterns[0], "Match ") {
			known = false
			continue
		}
		positive := false
		for _, pattern := range scope.patterns {
			pattern, negated := strings.CutPrefix(strings.ToLower(pattern), "!")
			if strings.ContainsAny(pattern, `\[]`) {
				pattern = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(pattern)
			}
			if ok, _ := path.Match(pattern, name); ok {
				if negated {
					return false, true
				}
				positive = true
			}
		}
		if !positive {
			return false, true
		}
	}
	return known, known
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
