// Package cli logs in to devices over SSH or Telnet and runs commands at their command line, for reading
// configurations that SNMP cannot provide. What differs between platforms (the prompt, how to turn off paging, which
// command prints the configuration) is described by a Profile; this package only drives the session.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	ProtocolSSH    = "SSH"
	ProtocolTelnet = "TELNET"

	// maxLoginSteps bounds the prompts answered while logging in, so a device that keeps asking cannot loop forever
	maxLoginSteps = 8
)

var (
	ErrAuthentication = errors.New("the device rejected the credentials")
	ErrEnable         = errors.New("could not enter privileged mode, check the enable password")

	usernamePrompt = regexp.MustCompile(`(?i)(user ?name|login|user)\s*:\s*$`)
	passwordPrompt = regexp.MustCompile(`(?i)pass(word|code)?\s*:\s*$`)
	anyKeyPrompt   = regexp.MustCompile(`(?i)press any key to continue`)
)

// Target is where and how to connect. HostKey is the SSH host key fingerprint the device must present, or empty to
// accept any. Timeout applies to connecting and to every wait for the device to respond.
type Target struct {
	Protocol string
	Host     string
	Port     uint16
	HostKey  string
	Timeout  time.Duration
}

// Credential logs in to a device. EnablePassword is only used on platforms with a separate privileged mode; when it
// is empty, the login password is tried instead, which many devices share.
type Credential struct {
	Username       string
	Password       string
	EnablePassword string
}

// Profile describes a platform's command line
type Profile struct {
	// Prompt matches the platform's command prompt, the last line of output whenever the device waits for a command
	Prompt *regexp.Regexp
	// UsernameSuffix is appended to the username to set login options, such as RouterOS turning off colors
	UsernameSuffix string
	// Newline ends each line sent over SSH, "\n" when empty. Telnet always sends "\r\n" (RFC 854).
	Newline string
	// Enable is the command that enters privileged mode when the prompt ends in ">", or empty when there is none
	Enable string
	// Commands returns the commands to run for the prompt seen after logging in. The output of the last one is the
	// result; the ones before it prepare the session (turning off paging, for example) and their output is ignored.
	Commands func(prompt string) []string
}

// Result is what Fetch read: the output of the profile's last command, and the SSH host key fingerprint the device
// presented (empty over Telnet)
type Result struct {
	Output  string
	HostKey string
}

// Fetch logs in to a device, runs the profile's commands and returns the output of the last one. Errors wrap
// ErrAuthentication when the credentials were rejected, so the caller can try others.
func Fetch(ctx context.Context, target Target, credential Credential, profile Profile) (Result, error) {
	var (
		conn    io.ReadWriteCloser
		hostKey string
		newline = "\r\n"
		err     error
	)

	username := credential.Username + profile.UsernameSuffix

	switch target.Protocol {
	case ProtocolSSH:
		conn, hostKey, err = dialSSH(ctx, target, credential, username)
		newline = profile.Newline

		if len(newline) < 1 {
			newline = "\n"
		}
	case ProtocolTelnet:
		conn, err = dialTelnet(ctx, target)
	default:
		err = fmt.Errorf("unsupported protocol %q, must be %s or %s", target.Protocol, ProtocolSSH, ProtocolTelnet)
	}

	if err != nil {
		return Result{HostKey: hostKey}, err
	}

	s := newShell(conn, newline, target.Timeout)

	defer s.close()

	output, err := runSession(ctx, s, credential, username, profile)

	return Result{Output: output, HostKey: hostKey}, err
}

func runSession(ctx context.Context, s *shell, credential Credential, username string, profile Profile) (string, error) {
	prompt, err := login(ctx, s, credential, username, profile.Prompt)

	if err != nil {
		return "", err
	}

	if len(profile.Enable) > 0 && strings.HasSuffix(strings.TrimSpace(prompt), ">") {
		if prompt, err = enable(ctx, s, credential, profile); err != nil {
			return "", err
		}
	}

	commands := profile.Commands(prompt)

	var output string

	for _, command := range commands {
		if output, err = s.run(ctx, command, profile.Prompt); err != nil {
			return "", err
		}
	}

	return output, nil
}

// login answers the username and password prompts a device shows (Telnet always, SSH only on devices that ask again
// after authenticating) until its command prompt appears, which it returns. A device that stays silent is sent a line
// break once, since some only show their prompt after one.
func login(ctx context.Context, s *shell, credential Credential, username string, prompt *regexp.Regexp) (string, error) {
	sentUsername, sentPassword, nudged := false, false, false

	for range maxLoginSteps {
		index, output, err := s.expect(ctx, prompt, usernamePrompt, passwordPrompt, anyKeyPrompt)

		if errors.Is(err, ErrTimeout) && !nudged {
			nudged = true

			if err = s.send(""); err != nil {
				return "", err
			}

			continue
		}

		if err != nil {
			return "", fmt.Errorf("logging in: %w", err)
		}

		switch index {
		case 0:
			return lastLine(output), nil
		case 1:
			// Being asked again means the device rejected what was sent
			if sentUsername {
				return "", ErrAuthentication
			}

			sentUsername = true
			err = s.send(username)
		case 2:
			if sentPassword {
				return "", ErrAuthentication
			}

			sentPassword = true
			err = s.send(credential.Password)
		case 3:
			err = s.send("")
		}

		if err != nil {
			return "", err
		}
	}

	return "", errors.New("logging in: the device kept asking for input without showing a command prompt")
}

// enable enters privileged mode and returns the new prompt
func enable(ctx context.Context, s *shell, credential Credential, profile Profile) (string, error) {
	password := credential.EnablePassword

	if len(password) < 1 {
		password = credential.Password
	}

	if err := s.send(profile.Enable); err != nil {
		return "", err
	}

	sentPassword := false

	for {
		index, output, err := s.expect(ctx, profile.Prompt, passwordPrompt)

		if err != nil {
			return "", fmt.Errorf("%s: %w", profile.Enable, err)
		}

		if index == 0 {
			prompt := lastLine(output)

			if strings.HasSuffix(strings.TrimSpace(prompt), ">") {
				return "", ErrEnable
			}

			return prompt, nil
		}

		if sentPassword {
			return "", ErrEnable
		}

		sentPassword = true

		if err = s.send(password); err != nil {
			return "", err
		}
	}
}
