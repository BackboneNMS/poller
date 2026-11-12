package cli

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	testUsername       = "backup"
	testPassword       = "hunter22"
	testEnablePassword = "s3cret"
	testConfig         = "!\nversion 15.2\nhostname switch\n!\ninterface GigabitEthernet0/1\n description uplink\n!\nend"
	testPagerLines     = 3
)

var testProfile = Profile{
	Prompt:   regexp.MustCompile(`^[\w.\-@/:()]+[>#] ?$`),
	Enable:   "enable",
	Commands: func(string) []string { return []string{"show running-config"} },
}

// fakeDevice is a Cisco-like command line: it echoes what it is sent, starts in user mode ("switch>"), asks for the
// enable password, and pages the configuration after testPagerLines lines unless "terminal length 0" was sent
type fakeDevice struct {
	reader *bufio.Reader
	writer io.Writer
	telnet bool
	paging bool
}

func (d *fakeDevice) write(text string) {
	io.WriteString(d.writer, text)
}

// readLine reads one line, echoing it the way a terminal does. Telnet negotiation from the client is skipped.
func (d *fakeDevice) readLine(echo bool) (string, error) {
	var line strings.Builder

	for {
		b, err := d.reader.ReadByte()

		if err != nil {
			return "", err
		}

		switch {
		case d.telnet && b == telnetIAC:
			d.reader.Discard(2)
		case b == '\r' || b == '\n':
			if b == '\r' {
				if next, err := d.reader.Peek(1); err == nil && next[0] == '\n' {
					d.reader.ReadByte()
				}
			}

			d.write("\r\n")

			return line.String(), nil
		default:
			line.WriteByte(b)

			if echo {
				d.writer.Write([]byte{b})
			}
		}
	}
}

func (d *fakeDevice) login() bool {
	for range 3 {
		d.write("\r\nUser Access Verification\r\n\r\nUsername: ")
		username, err := d.readLine(true)

		if err != nil {
			return false
		}

		d.write("Password: ")
		password, err := d.readLine(false)

		if err != nil {
			return false
		}

		if username == testUsername && password == testPassword {
			return true
		}

		d.write("% Authentication failed\r\n")
	}

	return false
}

func (d *fakeDevice) serve() {
	if d.telnet {
		d.writer.Write([]byte{telnetIAC, telnetWill, telnetOptionEcho, telnetIAC, telnetWill, telnetOptionSuppressGoAhead})

		if !d.login() {
			return
		}
	}

	d.paging = true
	prompt := "switch>"

	for {
		d.write("\r\n" + prompt)

		command, err := d.readLine(true)

		if err != nil {
			return
		}

		switch command {
		case "enable":
			d.write("Password: ")

			if password, _ := d.readLine(false); password == testEnablePassword {
				prompt = "switch#"
			} else {
				d.write("% Bad secrets\r\n")
			}
		case "terminal length 0":
			d.paging = false
		case "show running-config":
			if prompt != "switch#" {
				d.write("% Invalid input detected at '^' marker.\r\n")

				continue
			}

			d.write("Building configuration...\r\n\r\n")

			for i, line := range strings.Split(testConfig, "\n") {
				if d.paging && i > 0 && i%testPagerLines == 0 {
					d.write(" --More-- ")

					if b, err := d.reader.ReadByte(); err != nil || b != ' ' {
						return
					}

					d.write("\r          \r")
				}

				d.write(line + "\r\n")
			}
		case "":
		default:
			d.write("% Unknown command\r\n")
		}
	}
}

func listen(t *testing.T, handle func(net.Conn)) (string, uint16) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()

			if err != nil {
				return
			}

			go handle(conn)
		}
	}()

	address := listener.Addr().(*net.TCPAddr)

	return address.IP.String(), uint16(address.Port)
}

// startSSHDevice serves the fake device over SSH, returning its address and host key fingerprint
func startSSHDevice(t *testing.T) (string, uint16, string) {
	_, private, err := ed25519.GenerateKey(rand.Reader)

	if err != nil {
		t.Fatal(err)
	}

	signer, err := ssh.NewSignerFromKey(private)

	if err != nil {
		t.Fatal(err)
	}

	config := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if meta.User() == testUsername && string(password) == testPassword {
				return nil, nil
			}

			return nil, errors.New("access denied")
		},
	}

	config.AddHostKey(signer)

	host, port := listen(t, func(conn net.Conn) {
		_, channels, requests, err := ssh.NewServerConn(conn, config)

		if err != nil {
			return
		}

		go ssh.DiscardRequests(requests)

		for newChannel := range channels {
			channel, channelRequests, err := newChannel.Accept()

			if err != nil {
				return
			}

			go func() {
				for request := range channelRequests {
					request.Reply(request.Type == "pty-req" || request.Type == "shell", nil)
				}
			}()

			go func() {
				defer channel.Close()

				(&fakeDevice{reader: bufio.NewReader(channel), writer: channel}).serve()
			}()
		}
	})

	return host, port, ssh.FingerprintSHA256(signer.PublicKey())
}

func startTelnetDevice(t *testing.T) (string, uint16) {
	return listen(t, func(conn net.Conn) {
		defer conn.Close()

		(&fakeDevice{reader: bufio.NewReader(conn), writer: conn, telnet: true}).serve()
	})
}

func testCredential() Credential {
	return Credential{Username: testUsername, Password: testPassword, EnablePassword: testEnablePassword}
}

func fetchWithTimeout(t *testing.T, target Target, credential Credential) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

	defer cancel()

	target.Timeout = 3 * time.Second

	return Fetch(ctx, target, credential, testProfile)
}

func expectedTestConfig() string {
	return "Building configuration...\n\n" + testConfig
}

func TestFetchOverSSHEntersEnableModeAndAnswersThePager(t *testing.T) {
	host, port, fingerprint := startSSHDevice(t)

	result, err := fetchWithTimeout(t, Target{Protocol: ProtocolSSH, Host: host, Port: port}, testCredential())

	if err != nil {
		t.Fatal(err)
	}

	if result.Output != expectedTestConfig() {
		t.Fatalf("expected the configuration\n%s\ngot\n%s", expectedTestConfig(), result.Output)
	}

	if result.HostKey != fingerprint {
		t.Fatalf("expected the host key %s, got %s", fingerprint, result.HostKey)
	}
}

func TestFetchOverSSHRejectsWrongCredentials(t *testing.T) {
	host, port, _ := startSSHDevice(t)

	credential := testCredential()
	credential.Password = "wrong"

	if _, err := fetchWithTimeout(t, Target{Protocol: ProtocolSSH, Host: host, Port: port}, credential); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected an authentication error, got %v", err)
	}
}

func TestFetchOverSSHRefusesAChangedHostKey(t *testing.T) {
	host, port, fingerprint := startSSHDevice(t)

	result, err := fetchWithTimeout(t, Target{Protocol: ProtocolSSH, Host: host, Port: port, HostKey: "SHA256:previous"}, testCredential())

	var mismatch *HostKeyMismatchError

	if !errors.As(err, &mismatch) || mismatch.Received != fingerprint || result.HostKey != fingerprint {
		t.Fatalf("expected a host key mismatch reporting %s, got %v (%q)", fingerprint, err, result.HostKey)
	}

	if _, err = fetchWithTimeout(t, Target{Protocol: ProtocolSSH, Host: host, Port: port, HostKey: fingerprint}, testCredential()); err != nil {
		t.Fatalf("expected the recorded host key to be accepted, got %v", err)
	}
}

func TestFetchWithWrongEnablePassword(t *testing.T) {
	host, port, _ := startSSHDevice(t)

	credential := testCredential()
	credential.EnablePassword = "wrong"

	if _, err := fetchWithTimeout(t, Target{Protocol: ProtocolSSH, Host: host, Port: port}, credential); !errors.Is(err, ErrEnable) {
		t.Fatalf("expected an enable error, got %v", err)
	}
}

func TestFetchOverTelnetLogsIn(t *testing.T) {
	host, port := startTelnetDevice(t)

	result, err := fetchWithTimeout(t, Target{Protocol: ProtocolTelnet, Host: host, Port: port}, testCredential())

	if err != nil {
		t.Fatal(err)
	}

	if result.Output != expectedTestConfig() || len(result.HostKey) > 0 {
		t.Fatalf("expected the configuration and no host key, got %q (%q)", result.Output, result.HostKey)
	}
}

func TestFetchOverTelnetRejectsWrongCredentials(t *testing.T) {
	host, port := startTelnetDevice(t)

	credential := testCredential()
	credential.Password = "wrong"

	if _, err := fetchWithTimeout(t, Target{Protocol: ProtocolTelnet, Host: host, Port: port}, credential); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected an authentication error, got %v", err)
	}
}

func TestFetchUnreachable(t *testing.T) {
	host, port := listen(t, func(conn net.Conn) { conn.Close() })

	_, err := fetchWithTimeout(t, Target{Protocol: ProtocolSSH, Host: host, Port: port}, testCredential())

	if err == nil || errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected a connection error that is not an authentication error, got %v", err)
	}
}

func TestCleanTerminal(t *testing.T) {
	for raw, expected := range map[string]string{
		"\x1b[1;32mgreen\x1b[0m text\r\n":   "green text\n",
		" --More-- \r          \rnext line": "next line",
		"typo\b\bed":                        "tyed",
		"\x1b]0;title\x07prompt#":           "prompt#",
		"a\x00b\r\r\nc":                     "ab\nc",
	} {
		if cleaned := cleanTerminal(raw); cleaned != expected {
			t.Errorf("%q: expected %q, got %q", raw, expected, cleaned)
		}
	}
}

func TestCommandOutput(t *testing.T) {
	raw := "show running-config\r\n\r\nline one\r\n! show running-config\r\nline two  \r\n\r\nswitch#"

	if output := commandOutput([]byte(raw), "show running-config", testProfile.Prompt); output != "line one\n! show running-config\nline two" {
		t.Fatalf("unexpected output %q", output)
	}

	if output := commandOutput([]byte("switch#"), "show version", testProfile.Prompt); len(output) > 0 {
		t.Fatalf("expected no output, got %q", output)
	}
}

func TestCommandOutputRemovesRepeatedPrompts(t *testing.T) {
	prompt := regexp.MustCompile(`^\[[^\]]+@[^\]]+\] ?[\w/ -]*> ?$`)
	raw := "/export\r\n# software id = ABCD-1234\r\n/user aaa\r\nset use-radius=yes\r\n\r\n[admin@NVC Office] > \r\n[admin@NVC Office] > "

	if output := commandOutput([]byte(raw), "/export", prompt); output != "# software id = ABCD-1234\n/user aaa\nset use-radius=yes" {
		t.Fatalf("expected both prompts removed, got %q", output)
	}
}
