// Package mobile is the gomobile binding surface for Android. It exposes only
// strings and ints so the generated Java/Kotlin API stays trivial; all logic
// lives in package facade.
//
// Build: see Makefile target "aar".
package mobile

import "nightclip/core/facade"

// Core wraps one core instance.
type Core struct{ c *facade.Core }

// NewCore returns an uninitialised Core.
func NewCore() *Core { return &Core{c: facade.New()} }

// Init configures the core with the JSON described in docs/api-spec.md §7.2.
func (k *Core) Init(configJSON string) error { return k.c.Init(configJSON) }

// Call invokes a facade method and returns the JSON reply.
func (k *Core) Call(method, paramsJSON string) string { return k.c.Call(method, paramsJSON) }

// NextEvent blocks up to timeoutMs for the next event; returns "" on timeout.
func (k *Core) NextEvent(timeoutMs int) string { return k.c.NextEvent(timeoutMs) }

// Shutdown stops the core.
func (k *Core) Shutdown() { k.c.Shutdown() }
