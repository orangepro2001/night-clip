// Package agentops defines the broker protocol and executes its small set of
// approved, device-side operations.
package agentops

import (
	"net/url"
	"regexp"
	"strings"
)

type Request struct {
	ID         string `json:"id"`
	Client     string `json:"client"`
	Repository string `json:"repository"`
	Expires    int64  `json:"expires"`
}

type Result struct {
	ID         string      `json:"id"`
	State      string      `json:"state"`
	Repository *Repository `json:"repository,omitempty"`
}

type Repository struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
}

type DeviceCall struct {
	Action  string  `json:"action"`
	Client  string  `json:"client,omitempty"`
	Request Request `json:"request,omitempty"`
	Result  Result  `json:"result,omitempty"`
}

type DeviceReply struct {
	Token   string   `json:"token,omitempty"`
	Expires int64    `json:"expires,omitempty"`
	Request *Request `json:"request,omitempty"`
}

var part = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func ValidRepository(name string) bool {
	a, b, ok := strings.Cut(name, "/")
	return ok && a != "." && a != ".." && b != "." && b != ".." && part.MatchString(a) && part.MatchString(b)
}

func GitHubURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "github.com" || host == "api.github.com"
}
