package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"PhantomOps/internal/collector/ssh"
	"PhantomOps/internal/config"
	"PhantomOps/internal/remediate"
)

// sshConfigOf converts a saved or in-form SSH connection to the SSH settings.
func sshConfigOf(conn config.Connection) (ssh.Config, error) {
	if conn.Type != config.TypeSSH {
		return ssh.Config{}, errors.New("host keys can only be checked for SSH connections")
	}
	return ssh.Config{Addr: conn.Addr, User: conn.User, KeyPath: conn.KeyPath,
		KnownHosts: conn.KnownHosts, Insecure: conn.Insecure}, nil
}

// InspectSSHHostKey connects to the server just far enough to read its host
// key and reports whether known_hosts trusts it. It sends no credentials and
// changes nothing, so it is safe to call before saving the connection.
func (a *App) InspectSSHHostKey(conn config.Connection) (ssh.HostKeyInfo, error) {
	cfg, err := sshConfigOf(conn)
	if err != nil {
		return ssh.HostKeyInfo{}, err
	}
	ctx, cancel := context.WithTimeout(a.context(), 15*time.Second)
	defer cancel()
	return ssh.InspectHostKey(ctx, cfg)
}

// TrustSSHHostKey adds the server's host key to known_hosts. The caller must
// pass the fingerprint the user was shown and confirmed; the key is saved
// only if the server still presents exactly that key, and a changed key is
// never replaced. The intent is written to the audit log first; if that
// fails, nothing is changed.
func (a *App) TrustSSHHostKey(conn config.Connection, fingerprint string) (ssh.HostKeyInfo, error) {
	cfg, err := sshConfigOf(conn)
	if err != nil {
		return ssh.HostKeyInfo{}, err
	}
	entry := remediate.Entry{User: currentUser(), IncidentID: "", Action: "trust-host-key", Platform: "ssh",
		Connection: conn.Name, Target: cfg.Addr, Command: "append the server's host key to known_hosts", Message: fingerprint}

	entry.Phase = "approved"
	if err := a.audit.Append(entry); err != nil {
		return ssh.HostKeyInfo{}, fmt.Errorf("the host key was not saved because the action could not be written to the audit log: %w", err)
	}
	ctx, cancel := context.WithTimeout(a.context(), 20*time.Second)
	defer cancel()
	info, terr := ssh.TrustHostKey(ctx, cfg, fingerprint)

	entry.Phase, entry.OK = "result", terr == nil
	if terr != nil {
		entry.Message = fingerprint + ": " + terr.Error()
	} else {
		entry.Command = "added to " + info.KnownHostsPath
	}
	_ = a.audit.Append(entry) // the intent is already on record; a failure here cannot undo the change
	return info, terr
}
