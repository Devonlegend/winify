package deployment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
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

// DialSSH opens an SSH connection using key authentication and verifies the
// host key against knownHostsFile. The key is never logged.
func DialSSH(ctx context.Context, host string, port int, user, privateKeyPEM, knownHostsFile string) (*SSHRunner, error) {
	return DialSSHVerified(ctx, host, port, user, privateKeyPEM, knownHostsFile, false)
}

// DialSSHVerified is the explicit policy-bearing SSH dialer. An empty
// known_hosts file fails closed; callers must opt into
// DialSSHAllowInsecureHostKey for throwaway environments.
func DialSSHVerified(ctx context.Context, host string, port int, user, privateKeyPEM, knownHostsFile string, allowInsecure bool) (*SSHRunner, error) {
	if port == 0 {
		port = 22
	}
	signer, err := ssh.ParsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("parse ssh private key: %w", err)
	}

	var hostKey ssh.HostKeyCallback
	if strings.TrimSpace(knownHostsFile) != "" {
		hostKey, err = knownhosts.New(knownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts: %w", err)
		}
	} else if allowInsecure {
		hostKey = ssh.InsecureIgnoreHostKey()
	} else {
		return nil, errors.New("SSH known_hosts_file is required; refusing an unverified host key")
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

// syncBuffer is a mutex-protected bounded buffer. x/crypto/ssh copies stdout
// and stderr concurrently, so a plain bytes.Buffer shared by both is a data
// race. Keeping a head and tail preserves diagnostics and the final rev/backup
// line without allowing an unbounded command to exhaust memory.
type syncBuffer struct {
	mu        sync.Mutex
	head      []byte
	tail      []byte
	total     int
	truncated bool
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := len(p)
	s.total += original
	headLimit := maxCommandOutput / 2
	tailLimit := maxCommandOutput - headLimit
	if len(s.head) < headLimit {
		n := headLimit - len(s.head)
		if n > len(p) {
			n = len(p)
		}
		s.head = append(s.head, p[:n]...)
		p = p[n:]
	}
	if len(p) > 0 {
		s.tail = append(s.tail, p...)
		if len(s.tail) > tailLimit {
			s.tail = append([]byte(nil), s.tail[len(s.tail)-tailLimit:]...)
			s.truncated = true
		}
	}
	if s.total > maxCommandOutput {
		s.truncated = true
	}
	return original, nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.truncated {
		return string(append(append([]byte(nil), s.head...), s.tail...))
	}
	return string(s.head) + "\n...[command output truncated]...\n" + string(s.tail)
}

// Run executes command through the target's login shell and returns combined
// output. It honors ctx cancellation by closing the session.
func (r *SSHRunner) Run(ctx context.Context, command string) (string, error) {
	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var buf syncBuffer
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

// DialSSHAllowInsecureHostKey is reserved for explicitly isolated development
// targets. Production code should use DialSSH or DialSSHVerified instead.
func DialSSHAllowInsecureHostKey(ctx context.Context, host string, port int, user, privateKeyPEM string) (*SSHRunner, error) {
	return DialSSHVerified(ctx, host, port, user, privateKeyPEM, "", true)
}

// Close ends the SSH connection.
func (r *SSHRunner) Close() error { return r.client.Close() }
