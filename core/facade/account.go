package facade

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"moonpassword/core/proto"
	"moonpassword/core/store"
	moonsso "sso.moonl.cloud/sdk/go"
	"sso.moonl.cloud/sdk/go/desktop"
)

const accountIssuer = "https://sso.moonl.cloud"
const accountClientID = "ms_PpTVOukmuReXtAoouKhU" // Public native client ID provisioned in Moon SSO.
const accountSecret = "account.moon-sso.v1"

type accountStore struct{ db *store.DB }

func (s accountStore) Load(context.Context) (*moonsso.Stored, error) {
	data, found, err := s.db.GetSecret(accountSecret)
	if err != nil || !found {
		return nil, err
	}
	defer clear(data)
	var session moonsso.Stored
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s accountStore) Save(_ context.Context, session *moonsso.Stored) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	defer clear(data)
	return s.db.SetSecret(accountSecret, data)
}

func (s accountStore) Clear(context.Context) error { return s.db.DeleteSecret(accountSecret) }

type accountState struct {
	mu      sync.Mutex
	workers sync.WaitGroup
	ctx     context.Context
	stop    context.CancelFunc
	storage moonsso.Store
	issuer  string
	id      string
	notify  func()

	session *moonsso.Session
	login   *accountLogin
	failure string
}

type accountLogin struct {
	ctx    context.Context
	cancel context.CancelFunc
	url    chan string
	done   chan struct{}
}

type accountView struct {
	State    string `json:"state"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
}

func newAccountState(storage moonsso.Store, issuer, id string, notify func()) *accountState {
	ctx, stop := context.WithCancel(context.Background())
	return &accountState{ctx: ctx, stop: stop, storage: storage, issuer: issuer, id: id, notify: notify}
}

// ensureSession runs with mu held. The same installation key is used after
// restart so Moon SSO can renew the DPoP-bound refresh token.
func (a *accountState) ensureSession() error {
	if a.session != nil {
		return nil
	}
	stored, err := a.storage.Load(a.ctx)
	if err != nil {
		return err
	}
	var key *moonsso.DPoPKey
	if stored != nil {
		key, err = moonsso.ParseDPoPKey(stored.DPoPKey)
	} else {
		key, err = moonsso.NewDPoPKey()
	}
	if err != nil {
		return err
	}
	client, err := moonsso.NewClient(moonsso.Config{
		Issuer: a.issuer, ClientID: a.id, DPoP: key, UserAgent: "MoonPassword",
	})
	if err != nil {
		return err
	}
	a.session = client.NewSession(a.storage, moonsso.SessionOptions{OnSessionExpired: a.notify})
	session := a.session
	a.workers.Go(func() { session.KeepFresh(a.ctx) })
	return nil
}

func (a *accountState) status() accountView {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.id == "":
		return accountView{State: "unavailable"}
	case a.ctx.Err() != nil:
		return accountView{State: "unavailable"}
	case a.ensureSession() != nil:
		return accountView{State: "signed_out", Error: "session_storage"}
	case a.login != nil:
		return accountView{State: "authorizing"}
	}
	if profile := a.session.Profile(a.ctx); profile != nil {
		return accountView{State: "signed_in", Name: profile.Name, Username: profile.PreferredUsername}
	}
	return accountView{State: "signed_out", Error: a.failure}
}

func (a *accountState) begin() (string, error) {
	a.mu.Lock()
	switch {
	case a.ctx.Err() != nil || a.id == "":
		a.mu.Unlock()
		return "", proto.Errorf(proto.CodeInvalid, "Moon SSO is not configured")
	case a.ensureSession() != nil:
		a.mu.Unlock()
		return "", proto.Errorf(proto.CodeInternal, "cannot load the local Moon SSO session")
	case a.login != nil || a.session.SignedIn(a.ctx):
		a.mu.Unlock()
		return "", proto.Errorf(proto.CodeInvalid, "a Moon SSO session is already active")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	login := &accountLogin{ctx: ctx, cancel: cancel, url: make(chan string, 1), done: make(chan struct{})}
	a.login, a.failure = login, ""
	session := a.session
	a.workers.Go(func() { a.authorize(login, session) })
	a.mu.Unlock()

	select {
	case url := <-login.url:
		return url, nil
	case <-login.done:
		return "", proto.Errorf(proto.CodeInternal, "could not start Moon SSO authorization")
	}
}

func (a *accountState) authorize(login *accountLogin, session *moonsso.Session) {
	defer close(login.done)
	defer login.cancel()
	token, claims, err := desktop.LoginWithBrowser(login.ctx, session.Client(), desktop.Options{
		Open: func(url string) error {
			login.url <- url
			return nil
		},
		Auth: moonsso.AuthOptions{Prompt: "select_account"},
	})
	a.mu.Lock()
	if a.login != login {
		a.mu.Unlock()
		return
	}
	a.login = nil
	if err != nil || login.ctx.Err() != nil {
		a.failure = "authorization_failed"
	} else if token.IDToken == "" || claims.String("sub") == "" {
		a.failure = "authorization_failed"
	} else if session.Begin(a.ctx, token, claims) != nil {
		a.failure = "session_storage"
	}
	a.mu.Unlock()
	a.notify()
}

func (a *accountState) cancelLogin() {
	a.mu.Lock()
	if a.login != nil {
		a.login.cancel()
		a.login = nil
	}
	a.failure = ""
	a.mu.Unlock()
	a.notify()
}

func (a *accountState) logout() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil {
		return context.Canceled
	}
	if a.login != nil {
		a.login.cancel()
		a.login = nil
	}
	a.failure = ""
	if a.session == nil {
		return a.storage.Clear(a.ctx)
	}
	return a.session.Logout(a.ctx)
}

func (a *accountState) close() {
	a.mu.Lock()
	a.stop()
	a.mu.Unlock()
	a.workers.Wait()
}

func (c *Core) accountCall(method string) (any, error) {
	c.mu.RLock()
	a := c.account
	c.mu.RUnlock()
	if a == nil {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	switch method {
	case "account.status":
		return a.status(), nil
	case "account.login":
		url, err := a.begin()
		if err != nil {
			return nil, err
		}
		return map[string]string{"authorization_url": url}, nil
	case "account.cancel":
		a.cancelLogin()
	case "account.logout":
		if err := a.logout(); err != nil {
			return nil, proto.Errorf(proto.CodeInternal, "could not clear the local Moon SSO session")
		}
		a.notify()
	}
	return a.status(), nil
}
