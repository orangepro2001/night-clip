package facade

import (
	"context"
	"encoding/json"

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
	ch := &controlChannel{srv: &pipe.Server{Call: c.pipeCall}, cancel: cancel, done: make(chan struct{})}
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

// pipeCall is Call as seen from the local pipe, whose callers are other processes of the
// same user and are therefore trusted less than the GUI:
//   - file.offer cannot hand over an open descriptor (that is for the Android picker);
//   - file.accept cannot choose where the file is written: it goes to the download folder
//     the user configured;
//   - the text of sensitive inbox items is withheld (the GUI shows it on request).
func (c *Core) pipeCall(method, paramsJSON string) string {
	switch method {
	case "file.offer":
		paramsJSON = withoutKeys(paramsJSON, "fd")
	case "file.accept":
		paramsJSON = withoutKeys(paramsJSON, "save_dir")
	}
	out := c.Call(method, paramsJSON)
	if method == "inbox.list" || method == "inbox.get" {
		out = redactSensitive(out)
	}
	return out
}

func withoutKeys(paramsJSON string, keys ...string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(paramsJSON), &m) != nil {
		return paramsJSON // Call reports the malformed params itself
	}
	for _, k := range keys {
		delete(m, k)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return paramsJSON
	}
	return string(b)
}

// redactSensitive blanks the text of every sensitive item in an inbox reply.
func redactSensitive(reply string) string {
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(reply), &env) != nil || !env.OK {
		return reply
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(env.Result, &res) != nil {
		return reply
	}
	hide := func(raw json.RawMessage) json.RawMessage {
		var it map[string]any
		if json.Unmarshal(raw, &it) != nil {
			return raw
		}
		if s, _ := it["sensitive"].(bool); s {
			it["text"] = ""
			it["redacted"] = true
		}
		b, err := json.Marshal(it)
		if err != nil {
			return raw
		}
		return b
	}
	if items, ok := res["items"]; ok { // inbox.list
		var list []json.RawMessage
		if json.Unmarshal(items, &list) == nil {
			for i := range list {
				list[i] = hide(list[i])
			}
			res["items"], _ = json.Marshal(list)
		}
	} else { // inbox.get: the result is the item itself
		it := hide(env.Result)
		out, _ := json.Marshal(map[string]any{"ok": true, "result": it})
		return string(out)
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "result": res})
	return string(out)
}
