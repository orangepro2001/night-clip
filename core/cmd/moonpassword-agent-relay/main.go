// Command moonpassword-agent-relay connects Codex and Grok Bot to opted-in Windows devices.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"moonpassword/core/agentrelay"
	moonsso "sso.moonl.cloud/sdk/go"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18653", "loopback listener behind the TLS reverse proxy")
	dir := flag.String("data", "", "directory for connector token hashes")
	issuer := flag.String("issuer", "https://sso.moonl.cloud", "Moon SSO issuer")
	clientID := flag.String("client-id", "", "MoonPassword native client ID")
	flag.Parse()
	if *dir == "" || *clientID == "" {
		log.Fatal("-data and -client-id are required")
	}
	identity, err := moonsso.NewClient(moonsso.Config{Issuer: *issuer, ClientID: *clientID})
	if err != nil {
		log.Fatal(err)
	}
	service, err := agentrelay.New(*dir, func(ctx context.Context, bearer string) (string, error) {
		claims, err := identity.VerifyAccessToken(ctx, bearer)
		if err != nil {
			return "", err
		}
		if claims.String("client_id") != *clientID || claims.String("sid") == "" ||
			!slices.Contains(strings.Fields(claims.String("scope")), "openid") {
			return "", errors.New("not an interactive MoonPassword session")
		}
		return claims.Subject(), nil
	})
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *listen, Handler: service, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		ctx, done := context.WithTimeout(context.Background(), 8*time.Second)
		defer done()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("MoonPassword agent relay listening on %s", *listen)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
