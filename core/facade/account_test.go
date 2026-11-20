package facade

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestAccountBrowserAuthorizationAndLogout(t *testing.T) {
	const clientID = "test-native-client"
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := jose.JSONWebKey{Key: &signingKey.PublicKey, KeyID: "test", Algorithm: "ES256", Use: "sig"}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: signingKey},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", jwk.KeyID))
	if err != nil {
		t.Fatal(err)
	}
	var auth atomic.Pointer[url.Values]
	var revoked atomic.Bool
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"revocation_endpoint": issuer + "/revoke", "jwks_uri": issuer + "/jwks",
			})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
		case "/token":
			q := auth.Load()
			if q == nil || r.ParseForm() != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				if r.Form.Get("refresh_token") != "refresh-test" || r.Header.Get("DPoP") == "" {
					t.Error("renewal did not carry the original refresh token and a DPoP proof")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "access-test-2", "refresh_token": "refresh-test-2", "expires_in": 3600, "token_type": "Bearer",
				})
				return
			}
			verifier := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "issued-code" || r.Form.Get("grant_type") != "authorization_code" ||
				r.Form.Get("redirect_uri") != q.Get("redirect_uri") || r.Form.Get("client_id") != clientID ||
				base64.RawURLEncoding.EncodeToString(verifier[:]) != q.Get("code_challenge") || r.Header.Get("DPoP") == "" {
				t.Error("exchange did not carry the authorization parameters and DPoP proof")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			now := time.Now()
			id, err := jwt.Signed(signer).Claims(jwt.Claims{
				Issuer: issuer, Subject: "pairwise-id", Audience: jwt.Audience{clientID},
				IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Hour)),
			}).Claims(map[string]any{"nonce": q.Get("nonce"), "name": "Vault Tester", "preferred_username": "tester"}).Serialize()
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-test", "refresh_token": "refresh-test", "id_token": id, "expires_in": 3600, "token_type": "Bearer",
			})
		case "/revoke":
			revoked.Store(r.ParseForm() == nil && r.Form.Get("token") == "refresh-test-2")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	n := startNode(t, "Desk", "windows")
	storage := accountStore{n.c.db}
	a := newAccountState(storage, issuer, clientID, func() {})
	defer a.close()

	authorization, err := a.begin()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != clientID || q.Get("code_challenge_method") != "S256" ||
		q.Get("dpop_jkt") == "" || a.status().State != "authorizing" {
		t.Fatal("unexpected authorization request or login state")
	}
	auth.Store(&q)
	callback := q.Get("redirect_uri")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(callback + "?state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || a.status().State != "authorizing" {
		t.Fatal("unrelated callback should not finish the current attempt")
	}
	code := url.Values{"code": {"issued-code"}, "state": {q.Get("state")}, "iss": {issuer}}
	response, err = client.Get(callback + "?" + code.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	waitFor(t, "the OAuth account signs in", func() bool { return a.status().State == "signed_in" })
	if state := a.status(); state.Name != "Vault Tester" || state.Username != "tester" {
		t.Fatalf("account profile: %+v", state)
	}
	a.close()
	restarted := newAccountState(storage, issuer, clientID, func() {})
	defer restarted.close()
	if restarted.status().State != "signed_in" {
		t.Fatal("the sealed session did not reopen")
	}
	if err := restarted.session.Refresh(restarted.ctx); err != nil {
		t.Fatalf("renew stored session: %v", err)
	}
	if err := restarted.logout(); err != nil || !revoked.Load() || restarted.status().State != "signed_out" {
		t.Fatalf("logout failed: %v", err)
	}
	if _, found, err := n.c.db.GetSecret(accountSecret); found || err != nil {
		t.Fatalf("logout left a stored session: %v", err)
	}
}
