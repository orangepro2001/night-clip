package facade

import (
	"errors"
	"runtime"
	"time"

	"golang.org/x/crypto/ssh"

	"moonpassword/core/sshagent"
	"moonpassword/core/sshagenthost"
)

func (c *Core) sshIndependent() bool {
	return runtime.GOOS == "windows" && c.cfg.Platform == "windows" && c.cfg.SSHAgentHostPath != ""
}

func (c *Core) sshRemote() *sshagenthost.Client {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	return c.ssh.remote
}

func (c *Core) sshRunningLocked() bool {
	return c.ssh.srv != nil || c.ssh.remote != nil && c.ssh.state == sshStateListening
}

// sshStartRemoteLocked runs with ssh.mu held. Configure is idempotent, so a
// restarted UI can reattach without reopening the public named pipe.
func (c *Core) sshStartRemoteLocked(name string) {
	key, err := parseDBKey(c.cfg)
	if err != nil {
		c.sshReportLocked(sshStateError, name, err.Error())
		return
	}
	secret := sshagenthost.Secret(key)
	clear(key)
	defer clear(secret)
	client, err := sshagenthost.Connect(c.cfg.SSHAgentHostPath, c.cfg.DataDir, secret)
	if err != nil {
		c.sshReportLocked(sshStateError, name, err.Error())
		return
	}
	if err := client.Configure(name, c.sshSettingValue(settingSSHForwarding) == "on"); err != nil {
		client.Close()
		c.sshReportLocked(sshStateError, name, err.Error())
		return
	}
	c.ssh.remote = client
	status := client.Cached()
	c.sshReportLocked(status.State, status.Pipe, status.Error)
	go c.sshPollRemote(client)
	go c.sshLoad()
}

func (c *Core) sshReportLocked(state, name, errorText string) {
	c.ssh.state, c.ssh.pipe, c.ssh.err = state, name, errorText
	c.emit(map[string]any{"type": "ssh.agent", "state": state, "pipe": name, "error": errorText})
}

func (c *Core) sshConfigureRemote(client *sshagenthost.Client) {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	if c.ssh.remote != client {
		return
	}
	name := c.sshSettingValue(settingSSHAgentPipe)
	err := client.Configure(name, c.sshSettingValue(settingSSHForwarding) == "on")
	if err != nil {
		client.Close()
		c.ssh.remote = nil
		c.sshReportLocked(sshStateError, name, err.Error())
		return
	}
	status := client.Cached()
	c.sshReportLocked(status.State, status.Pipe, status.Error)
}

func (c *Core) sshRemoteFailure(client *sshagenthost.Client, err error) {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	if c.ssh.remote == client {
		c.ssh.remote = nil
		client.Close()
		c.sshReportLocked(sshStateError, c.ssh.pipe, err.Error())
	}
}

func (c *Core) sshPollRemote(client *sshagenthost.Client) {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			events, err := client.Poll()
			if err != nil {
				c.sshRemoteFailure(client, err)
				return
			}
			for _, ev := range events {
				switch ev.Type {
				case "use":
					if len(ev.HostKey) > 0 {
						ev.Use.HostKey, _ = ssh.ParsePublicKey(ev.HostKey)
					}
					c.sshOnUse(ev.Use)
				case "needed":
					if c.ssh.selfTest.Load() == 0 {
						c.sshKeysNeeded(ev.Client)
					}
				case "expired", "cleared":
					c.sshKeysEvent(ev.Type, ev.Reason)
				}
			}
		}
	}
}

func (c *Core) sshSnapshot() ([]*sshagent.Key, string, time.Time) {
	if client := c.sshRemote(); client != nil {
		status := client.Cached()
		keys := make([]*sshagent.Key, 0, len(status.Keys))
		for _, k := range status.Keys {
			pub, err := ssh.ParsePublicKey(k.Public)
			if err != nil {
				continue
			}
			keys = append(keys, &sshagent.Key{Public: pub, EntryID: k.EntryID, Title: k.Title, Comment: k.Comment,
				Hosts: k.Hosts, Fingerprint: ssh.FingerprintSHA256(pub)})
		}
		return keys, status.KeysState, status.Expires
	}
	if c.ssh.ring == nil {
		return nil, sshagent.StateEmpty, time.Time{}
	}
	return c.ssh.ring.Snapshot()
}

func (c *Core) sshPrepareUpdate(keep bool) (bool, error) {
	c.ssh.mu.Lock()
	client, state, wasUpdating := c.ssh.remote, c.ssh.state, c.ssh.updating
	c.ssh.mu.Unlock()
	if client == nil || state != sshStateListening {
		return false, nil
	}
	if !keep {
		if !wasUpdating {
			return false, nil
		}
		err := client.CancelHandover()
		c.ssh.mu.Lock()
		c.ssh.updating = false
		c.ssh.mu.Unlock()
		return false, err
	}

	var expires time.Time
	c.vs.mu.Lock()
	if c.vs.v != nil {
		idle := time.Duration(c.vaultIntSetting(settingVaultAutoLock, defaultVaultAutoLock)) * time.Second
		if idle > 0 && !c.vs.lastActive.IsZero() {
			expires = c.vs.lastActive.Add(idle + c.sshTTL())
		}
	}
	c.vs.mu.Unlock()
	kept, err := client.Handover(expires)
	if err == nil && !kept {
		err = errors.New("the SSH Agent could not prepare for an update")
	}
	c.ssh.mu.Lock()
	c.ssh.updating = kept && c.ssh.remote == client
	c.ssh.mu.Unlock()
	return kept, err
}

func (c *Core) sshShutdownRemote() bool {
	c.ssh.mu.Lock()
	client, update := c.ssh.remote, c.ssh.updating
	if client == nil {
		c.ssh.mu.Unlock()
		return false
	}
	c.ssh.remote = nil
	if c.ssh.loadTimer != nil {
		c.ssh.loadTimer.Stop()
	}
	c.ssh.mu.Unlock()

	kept := false
	if update {
		kept, _ = client.CommitHandover()
	}
	if !kept {
		_, _ = client.Clear()
	}
	client.Close()
	return true
}
