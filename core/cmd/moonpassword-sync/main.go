// Command moonpassword-sync serves encrypted MoonPassword vault snapshots.
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

	"moonpassword/core/vaultsync"
	moonsso "sso.moonl.cloud/sdk/go"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18652", "HTTP listener behind the HTTPS reverse proxy")
	dir := flag.String("data", "", "directory for encrypted snapshots")
	issuer := flag.String("issuer", "https://sso.moonl.cloud", "Moon SSO issuer")
	clientID := flag.String("client-id", "", "MoonPassword public OAuth client ID")
	flag.Parse()
	if *dir == "" || *clientID == "" {
		log.Fatal("-data and -client-id are required")
	}
	identity, err := moonsso.NewClient(moonsso.Config{Issuer: *issuer, ClientID: *clientID})
	if err != nil {
		log.Fatal(err)
	}
	service, err := vaultsync.NewServer(*dir, func(ctx context.Context, bearer string) (string, error) {
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
	httpServer := &http.Server{
		Addr: *listen, Handler: service,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second,
		WriteTimeout: 90 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = httpServer.Shutdown(ctx)
	}()
	log.Printf("encrypted vault service listening on %s", *listen)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
