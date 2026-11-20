package facade

import (
	"crypto/subtle"
	"encoding/json"
	"net/url"
	"time"

	"moonpassword/core/proto"
	moonsso "sso.moonl.cloud/sdk/go"
)

const (
	mobileAccountSecret = "account.moon-sso.pending.v1"
	mobileRedirect      = "cloud.moonl.password:/oauth2redirect"
)

// Android can kill the process while the browser is open. Keep the PKCE
// verifier and the matching DPoP key together in sealed local storage.
type pendingMobileAccount struct {
	Request moonsso.AuthRequest `json:"request"`
	Key     string              `json:"dpop_key"`
	Expires time.Time           `json:"expires"`
}

func (a *accountState) loadMobile() (*pendingMobileAccount, error) {
	data, ok, err := a.mobile.GetSecret(mobileAccountSecret)
	if !ok || err != nil {
		return nil, err
	}
	defer clear(data)
	var pending pendingMobileAccount
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, err
	}
	if !time.Now().Before(pending.Expires) {
		return nil, a.mobile.DeleteSecret(mobileAccountSecret)
	}
	return &pending, nil
}

func (a *accountState) beginMobile() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil || a.id == "" || a.ensureSession() != nil || a.session.SignedIn(a.ctx) {
		return "", proto.Errorf(proto.CodeInvalid, "Moon SSO authorization is unavailable")
	}
	key, err := moonsso.NewDPoPKey()
	if err != nil {
		return "", proto.Errorf(proto.CodeInternal, "could not create the authorization key")
	}
	client, err := moonsso.NewClient(moonsso.Config{Issuer: a.issuer, ClientID: a.id, DPoP: key})
	if err != nil {
		return "", proto.Errorf(proto.CodeInternal, "could not start Moon SSO authorization")
	}
	request, err := client.AuthCodeURL(a.ctx, moonsso.AuthOptions{RedirectURL: mobileRedirect, Prompt: "select_account"})
	if err != nil {
		return "", proto.Errorf(proto.CodeInternal, "could not start Moon SSO authorization")
	}
	private, err := moonsso.MarshalDPoPKey(key)
	if err != nil {
		return "", proto.Errorf(proto.CodeInternal, "could not save the authorization key")
	}
	data, err := json.Marshal(pendingMobileAccount{Request: *request, Key: private, Expires: time.Now().Add(5 * time.Minute)})
	if err != nil {
		return "", err
	}
	defer clear(data)
	if err := a.mobile.SetSecret(mobileAccountSecret, data); err != nil {
		return "", err
	}
	a.failure = ""
	return request.URL, nil
}

func (a *accountState) completeMobile(callback string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mobile == nil || a.ctx.Err() != nil || len(callback) > 8192 {
		return proto.Errorf(proto.CodeInvalid, "unexpected Moon SSO callback")
	}
	u, err := url.Parse(callback)
	if err != nil || u.Scheme != "cloud.moonl.password" || u.Host != "" || u.User != nil ||
		u.Opaque != "" || u.EscapedPath() != "/oauth2redirect" || u.Fragment != "" {
		return proto.Errorf(proto.CodeInvalid, "unexpected Moon SSO callback")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["state"]) != 1 || len(query["iss"]) != 1 || query.Get("iss") != a.issuer {
		return proto.Errorf(proto.CodeInvalid, "unexpected Moon SSO callback")
	}
	pending, err := a.loadMobile()
	if err != nil {
		return proto.Errorf(proto.CodeInternal, "could not load the pending authorization")
	}
	if pending == nil || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(pending.Request.State)) != 1 {
		return proto.Errorf(proto.CodeInvalid, "no matching Moon SSO authorization")
	}
	if err := a.mobile.DeleteSecret(mobileAccountSecret); err != nil {
		return err
	}
	key, err := moonsso.ParseDPoPKey(pending.Key)
	if err != nil {
		a.failure = "session_storage"
		return nil
	}
	client, err := moonsso.NewClient(moonsso.Config{Issuer: a.issuer, ClientID: a.id, DPoP: key})
	if err != nil {
		a.failure = "authorization_failed"
		return nil
	}
	token, claims, err := client.Exchange(a.ctx, &pending.Request, query)
	if err != nil || token.IDToken == "" || claims.String("sub") == "" {
		a.failure = "authorization_failed"
		return nil
	}
	a.useClient(client)
	if err := a.session.Begin(a.ctx, token, claims); err != nil {
		a.failure = "session_storage"
	}
	return nil
}
