package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

const (
	// maxOutputBytes bounds what is kept of a single command's output, so a device that never stops talking cannot
	// exhaust memory. Configurations are far smaller, even on large chassis.
	maxOutputBytes = 16 << 20

	// settleDelay is how long the output must stay quiet after a prompt appears before it is believed. Large outputs
	// arrive in pieces, and one could end on a line that looks like a prompt; more data arriving shows it was not.
	settleDelay = 250 * time.Millisecond

	readBufferBytes = 32 << 10
)

// pagerPrompt matches the prompts devices show when they pause long output, which are answered with a space. Paging
// is turned off before the configuration is read, so this only catches devices where that did not work.
var pagerPrompt = regexp.MustCompile(`(?i)--\s*more\s*--|<--- more --->|press any key to continue`)

var ErrTimeout = errors.New("timed out waiting for the device")

// shell drives an interactive terminal session: it sends lines and waits for the output to end in an expected prompt.
// Output is read in the background so a device that keeps writing never blocks on a full window.
type shell struct {
	conn    io.ReadWriteCloser
	newline string
	timeout time.Duration
	chunks  chan []byte
	done    chan struct{}
	err     error
	buffer  []byte
}

func newShell(conn io.ReadWriteCloser, newline string, timeout time.Duration) *shell {
	s := &shell{
		conn:    conn,
		newline: newline,
		timeout: timeout,
		chunks:  make(chan []byte, 64),
		done:    make(chan struct{}),
	}

	go s.read()

	return s
}

func (s *shell) read() {
	for {
		data := make([]byte, readBufferBytes)
		n, err := s.conn.Read(data)

		if n > 0 {
			select {
			case s.chunks <- data[:n]:
			case <-s.done:
				return
			}
		}

		if err != nil {
			s.err = err
			close(s.chunks)

			return
		}
	}
}

// send writes a line to the device
func (s *shell) send(line string) error {
	_, err := io.WriteString(s.conn, line+s.newline)

	return err
}

// expect waits until the last line of the output matches one of patterns, answering pagers along the way, and returns
// the index of the pattern and everything received since the previous call. It fails when the device stays silent for
// longer than the timeout without matching, or closes the connection.
func (s *shell) expect(ctx context.Context, patterns ...*regexp.Regexp) (int, []byte, error) {
	timer := time.NewTimer(s.timeout)

	defer timer.Stop()

	for {
		line := lastLine(s.buffer)

		if pagerPrompt.MatchString(line) {
			if _, err := io.WriteString(s.conn, " "); err != nil {
				return -1, nil, err
			}

			s.dropLastLine()
		} else if index := matchIndex(patterns, line); index >= 0 && s.settled(ctx) {
			output := s.buffer
			s.buffer = nil

			return index, output, nil
		}

		select {
		case chunk, ok := <-s.chunks:
			if !ok {
				return -1, nil, fmt.Errorf("the device closed the connection (last output %q): %w", lastLine(s.buffer), s.err)
			}

			if len(s.buffer)+len(chunk) > maxOutputBytes {
				return -1, nil, fmt.Errorf("the output is larger than %d MiB", maxOutputBytes>>20)
			}

			s.buffer = append(s.buffer, chunk...)
			timer.Reset(s.timeout)
		case <-timer.C:
			return -1, nil, fmt.Errorf("%w after %s (last output %q)", ErrTimeout, s.timeout, lastLine(s.buffer))
		case <-ctx.Done():
			return -1, nil, ctx.Err()
		}
	}
}

// settled waits for the output to stay quiet for settleDelay, adding anything that arrives meanwhile to the buffer.
// It reports false when more output arrived, meaning the prompt that matched was part of the output.
func (s *shell) settled(ctx context.Context) bool {
	select {
	case chunk, ok := <-s.chunks:
		if !ok {
			// Nothing more can arrive, so the prompt was the end of the output
			return true
		}

		s.buffer = append(s.buffer, chunk...)

		return false
	case <-time.After(settleDelay):
		return true
	case <-ctx.Done():
		return true
	}
}

// dropLastLine removes the incomplete last line from the buffer, which is a pager prompt that has been answered
func (s *shell) dropLastLine() {
	for i := len(s.buffer) - 1; i >= 0; i-- {
		if s.buffer[i] == '\n' {
			s.buffer = s.buffer[:i+1]

			return
		}
	}

	s.buffer = s.buffer[:0]
}

// run sends a command and returns what it printed before the prompt came back
func (s *shell) run(ctx context.Context, command string, prompt *regexp.Regexp) (string, error) {
	if err := s.send(command); err != nil {
		return "", err
	}

	_, output, err := s.expect(ctx, prompt)

	if err != nil {
		return "", fmt.Errorf("%s: %w", command, err)
	}

	return commandOutput(output, command, prompt), nil
}

func (s *shell) close() error {
	close(s.done)

	return s.conn.Close()
}

func matchIndex(patterns []*regexp.Regexp, line string) int {
	for i, pattern := range patterns {
		if pattern.MatchString(line) {
			return i
		}
	}

	return -1
}
