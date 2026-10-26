package facade

import (
	"context"

	"nightclip/core/pipe"
)

// controlChannel is the local pipe/socket server of docs/api-spec.md §8.
type controlChannel struct {
	srv    *pipe.Server
	cancel context.CancelFunc
	done   chan struct{}
}

// startControlChannel starts the local control channel. It is optional: when the
// address is taken (another process squatting the name) the core carries on
// without it and tells the shell, because the GUI does not depend on it.
func (c *Core) startControlChannel(address string) {
	if address == "" {
		return
	}
	l, err := pipe.Listen(address)
	if err != nil {
		c.emitNet(map[string]any{"type": "core.warning", "code": "PIPE_UNAVAILABLE"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := &controlChannel{srv: &pipe.Server{Call: c.Call}, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(ch.done)
		_ = ch.srv.Serve(ctx, l)
	}()
	c.ctl = ch
}

func (ch *controlChannel) close() {
	ch.cancel()
	ch.srv.Close()
	<-ch.done
}
