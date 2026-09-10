package deployment

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHRunner runs commands on a Linux/Docker host over SSH. It can do anything
// the configured SSH user can do there — clone repos, build images and run
// containers. Treat target credentials as highly privileged.
type SSHRunner struct {
	client *ssh.Client
}

// DialSSH opens an SSH connection using key authentication. privateKeyPEM is
// the decrypted key from the credential store. knownHostsFile may be empty, in
// which case host keys are NOT verified (insecure; log a warning at the call
// site). The key is never logged.
func DialSSH(ctx context.Context, host string, port int, user, privateKeyPEM, knownHostsFile string) (*SSHRunner, error) {
	if port == 0 {
		port = 22
	}
	signer, err := ssh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("parse ssh private key: %w", err)
	}

	var hostKey ssh.HostKeyCallback
	if knownHostsFile != "" {
		hostKey, err = knownhosts.New(knownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts: %w", err)
		}
	} else {
		hostKey = ssh.InsecureIgnoreHostKey()
	}

	clientCfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKey,
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientCfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	return &SSHRunner{client: ssh.NewClient(clientConn, chans, reqs)}, nil
}

// Run executes command through the target's login shell and returns combined
// output. It honors ctx cancellation by closing the session.
func (r *SSHRunner) Run(ctx context.Context, command string) (string, error) {
	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return buf.String(), ctx.Err()
	case err := <-done:
		return buf.String(), err
	}
}

// Close ends the SSH connection.
func (r *SSHRunner) Close() error { return r.client.Close() }
