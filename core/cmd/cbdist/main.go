// Command cbdist runs on the release machine: it generates the release
// signing key, publishes builds into a release directory and serves that
// directory to the apps (docs/update.md).
//
//	cbdist keygen  -key FILE
//	cbdist publish -dir DIR -key FILE -platform android|windows -file PKG -code N -name VER [-notes TEXT] [-force]
//	cbdist serve   -dir DIR [-addr 127.0.0.1:PORT] [-prefix /clipbridge]
//	cbdist verify  -dir DIR -platform P            (checks the published manifest and file)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nightclip/core/update"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "publish":
		err = publish(os.Args[2:])
	case "serve":
		err = serve(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cbdist:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cbdist keygen|publish|serve|verify [flags]  (cbdist <command> -h for flags)")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	key := fs.String("key", "", "where to write the private key (must not exist)")
	_ = fs.Parse(args)
	if *key == "" {
		return errors.New("-key is required")
	}
	if err := os.MkdirAll(filepath.Dir(*key), 0o700); err != nil {
		return err
	}
	pub, err := update.GenerateKeyFile(*key)
	if err != nil {
		return err
	}
	fmt.Println(pub)
	fmt.Fprintln(os.Stderr, "private key written to", *key, "- back it up; put the public key above into core/update/keys.go")
	return nil
}

func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	dir := fs.String("dir", "", "release directory")
	key := fs.String("key", "", "private key file")
	platform := fs.String("platform", "", "android|windows")
	file := fs.String("file", "", "package to publish (.apk or .zip)")
	code := fs.Int64("code", 0, "versionCode (must be higher than the published one)")
	name := fs.String("name", "", "versionName")
	notes := fs.String("notes", "", "release notes")
	force := fs.Bool("force", false, "make this release mandatory for older apps")
	_ = fs.Parse(args)
	if *dir == "" || *key == "" || *platform == "" || *file == "" || *code <= 0 || *name == "" {
		return errors.New("-dir, -key, -platform, -file, -code and -name are required")
	}
	priv, err := update.LoadPrivateKey(*key)
	if err != nil {
		return err
	}
	m, err := update.Publish(update.PublishOptions{
		Dir: *dir, Platform: *platform, Source: *file, VersionCode: *code, VersionName: *name,
		Notes: *notes, Force: *force, Key: priv,
	})
	if err != nil {
		return err
	}
	fmt.Printf("published %s %s (%d) -> %s  sha256=%s  min=%d\n",
		m.Platform, m.VersionName, m.VersionCode, m.File, m.SHA256, m.MinVersionCode)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	dir := fs.String("dir", "", "release directory")
	platform := fs.String("platform", "", "android|windows")
	_ = fs.Parse(args)
	m, err := update.VerifyPublished(*dir, *platform, update.TrustedKeys())
	if err != nil {
		return err
	}
	fmt.Printf("ok: %s %s (%d), signature and %s verify\n", m.Platform, m.VersionName, m.VersionCode, m.File)
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := fs.String("dir", "", "release directory")
	addr := fs.String("addr", "127.0.0.1:18780", "listen address (loopback only)")
	prefix := fs.String("prefix", "", "path prefix kept by the reverse proxy")
	_ = fs.Parse(args)
	if *dir == "" {
		return errors.New("-dir is required")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("refusing to listen on %q: bind 127.0.0.1 and publish through tailscale serve", *addr)
	}
	srv := &http.Server{
		Addr: *addr, Handler: update.NewHandler(*dir, *prefix),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	fmt.Fprintf(os.Stderr, "cbdist: serving %s on http://%s\n", *dir, *addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
