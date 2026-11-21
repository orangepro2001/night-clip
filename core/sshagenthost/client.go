package sshagenthost

import (
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Client holds the authenticated owner connection. The cached status contains
// public information only, so entry lists never need one remote call per entry.
type Client struct {
	ch       *channel
	mu       sync.Mutex
	snapshot atomic.Pointer[Status]
}

func Attach(conn net.Conn, secret []byte) (*Client, error) {
	ch, err := authenticate(conn, secret, false)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = ch.SetReadDeadline(time.Now().Add(callTimeout))
	var ready response
	if err := ch.read(&ready); err != nil {
		conn.Close()
		return nil, err
	}
	if ready.Busy {
		conn.Close()
		return nil, errOwnerBusy
	}
	if ready.Error != "" {
		conn.Close()
		return nil, errors.New(ready.Error)
	}
	c := &Client{ch: ch}
	c.snapshot.Store(&ready.Status)
	return c, nil
}

func (c *Client) Cached() Status { return *c.snapshot.Load() }

func (c *Client) call(req request) (response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var r response
	_ = c.ch.SetDeadline(time.Now().Add(callTimeout))
	if err := c.ch.send(req); err != nil {
		c.ch.Close()
		return r, err
	}
	if err := c.ch.read(&r); err != nil {
		c.ch.Close()
		return r, err
	}
	c.snapshot.Store(&r.Status)
	if r.Error != "" {
		return r, errors.New(r.Error)
	}
	return r, nil
}

func (c *Client) Close() { _ = c.ch.Close() }

func (c *Client) Configure(pipe string, forward bool) error {
	_, err := c.call(request{Op: "configure", Pipe: pipe, Forward: forward, OwnerPID: uint32(os.Getpid())})
	return err
}

func (c *Client) Poll() ([]Event, error) {
	res, err := c.call(request{Op: "poll"})
	return res.Events, err
}

func (c *Client) Generation() (uint64, error) {
	res, err := c.call(request{Op: "status"})
	return res.Status.Generation, err
}

func (c *Client) Replace(gen uint64, keys []KeyInput) (bool, error) {
	res, err := c.call(request{Op: "replace", Generation: gen, Keys: keys})
	return res.Changed, err
}

func (c *Client) Clear() (int, error) {
	res, err := c.call(request{Op: "clear"})
	return res.Cleared, err
}

func (c *Client) Grace(until time.Time) (bool, error) {
	res, err := c.call(request{Op: "grace", Until: until})
	return res.Changed, err
}

func (c *Client) Handover(until time.Time) (bool, error) {
	res, err := c.call(request{Op: "handover", Until: until})
	return res.Changed, err
}

// CommitHandover allows a clean update shutdown to leave the host running.
func (c *Client) CommitHandover() (bool, error) {
	res, err := c.call(request{Op: "commit_handover"})
	return res.Changed, err
}

// CancelHandover restores ordinary teardown if the updater could not start.
func (c *Client) CancelHandover() error {
	_, err := c.call(request{Op: "cancel_handover"})
	return err
}
