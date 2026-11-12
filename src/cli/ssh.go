package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	terminalType   = "vt100"
	terminalWidth  = 511
	terminalHeight = 1000
)

// HostKeyMismatchError means the device presented a different SSH host key than the one recorded when it was first
// backed up, which happens when the device is replaced or reinstalled, but also when something intercepts the
// connection. Credentials are never sent to a host whose key does not match.
type HostKeyMismatchError struct {
	Expected string
	Received string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("the SSH host key changed from %s to %s, so no credentials were sent; if the device was replaced or reinstalled, forget the stored host key in its settings", e.Expected, e.Received)
}

// sshConn is an interactive SSH shell session read and written as one stream
type sshConn struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

func (c *sshConn) Read(p []byte) (int, error) {
	return c.stdout.Read(p)
}

func (c *sshConn) Write(p []byte) (int, error) {
	return c.stdin.Write(p)
}

func (c *sshConn) Close() error {
	c.session.Close()

	return c.client.Close()
}

// dialSSH logs in over SSH and opens an interactive shell. hostKey is the SHA256 fingerprint the device must present,
// or empty to accept any key; the fingerprint it presented is returned either way. Older ciphers and key exchanges are
// offered after the modern ones, since network equipment often only supports those.
func dialSSH(ctx context.Context, target Target, credential Credential, username string) (*sshConn, string, error) {
	var presented string

	supported, insecure := ssh.SupportedAlgorithms(), ssh.InsecureAlgorithms()

	config := &ssh.ClientConfig{
		Config: ssh.Config{
			KeyExchanges: append(supported.KeyExchanges, insecure.KeyExchanges...),
			Ciphers:      append(supported.Ciphers, insecure.Ciphers...),
			MACs:         append(supported.MACs, insecure.MACs...),
		},
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(credential.Password),
			ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))

				// Questions that echo the answer ask for the username, the hidden ones for the password
				for i := range questions {
					if echos[i] {
						answers[i] = username
					} else {
						answers[i] = credential.Password
					}
				}

				return answers, nil
			}),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			presented = ssh.FingerprintSHA256(key)

			if len(target.HostKey) > 0 && presented != target.HostKey {
				return &HostKeyMismatchError{Expected: target.HostKey, Received: presented}
			}

			return nil
		},
		HostKeyAlgorithms: append(supported.HostKeys, insecure.HostKeys...),
		Timeout:           target.Timeout,
	}

	address := net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
	dialer := &net.Dialer{Timeout: target.Timeout}

	conn, err := dialer.DialContext(ctx, "tcp", address)

	if err != nil {
		return nil, "", err
	}

	// The handshake has no timeout of its own, so a device that accepts the connection but never answers would hang
	conn.SetDeadline(time.Now().Add(target.Timeout))

	clientConn, channels, requests, err := ssh.NewClientConn(conn, address, config)

	if err != nil {
		conn.Close()

		var mismatch *HostKeyMismatchError

		if errors.As(err, &mismatch) {
			return nil, presented, mismatch
		}

		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, presented, fmt.Errorf("%w: %v", ErrAuthentication, err)
		}

		return nil, presented, err
	}

	conn.SetDeadline(time.Time{})

	client := ssh.NewClient(clientConn, channels, requests)

	result, err := openShell(client)

	if err != nil {
		client.Close()

		return nil, presented, err
	}

	return result, presented, nil
}

func openShell(client *ssh.Client) (*sshConn, error) {
	session, err := client.NewSession()

	if err != nil {
		return nil, err
	}

	stdin, err := session.StdinPipe()

	if err != nil {
		session.Close()

		return nil, err
	}

	stdout, err := session.StdoutPipe()

	if err != nil {
		session.Close()

		return nil, err
	}

	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}

	if err = session.RequestPty(terminalType, terminalHeight, terminalWidth, modes); err != nil {
		session.Close()

		return nil, fmt.Errorf("requesting a terminal: %w", err)
	}

	if err = session.Shell(); err != nil {
		session.Close()

		return nil, fmt.Errorf("starting a shell: %w", err)
	}

	return &sshConn{client: client, session: session, stdin: stdin, stdout: stdout}, nil
}
