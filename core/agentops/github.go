package agentops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

var githubClient = &http.Client{
	Timeout:       8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// GitHubRepository issues one fixed, read-only request. Only selected metadata
// from a matching repository is returned to the broker, including on errors.
func GitHubRepository(ctx context.Context, name, secret string) (*Repository, error) {
	return githubRepository(ctx, githubClient, name, secret)
}

func githubRepository(ctx context.Context, client *http.Client, name, secret string) (*Repository, error) {
	if !ValidRepository(name) || strings.TrimSpace(secret) == "" {
		return nil, errors.New("invalid GitHub repository or credential")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "MoonPassword")
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("unable to reach GitHub")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("GitHub declined the request or the repository is unavailable")
	}
	var data Repository
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&data); err != nil ||
		!strings.EqualFold(data.FullName, name) || len(data.DefaultBranch) > 256 {
		return nil, errors.New("unexpected GitHub response")
	}
	return &data, nil
}
