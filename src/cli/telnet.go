package cli

import (
	"bytes"
	"context"
	"net"
	"strconv"
)

// Telnet commands and options (RFC 854, 857, 858)
const (
	telnetSE   = 240
	telnetSB   = 250
	telnetWill = 251
	telnetWont = 252
	telnetDo   = 253
	telnetDont = 254
	telnetIAC  = 255

	telnetOptionEcho            = 1
	telnetOptionSuppressGoAhead = 3
)

type telnetState int

const (
	telnetData telnetState = iota
	telnetCommand
	telnetOption
	telnetSubnegotiation
	telnetSubnegotiationIAC
)

// telnetConn is a Telnet connection that strips protocol commands out of what it reads. It lets the device echo and
// suppress go-ahead, which every network device expects, and refuses every other option (terminal type, window size),
// so the device falls back to a plain terminal. Refusals are not answered, which keeps negotiation from looping.
type telnetConn struct {
	net.Conn
	state   telnetState
	command byte
}

func dialTelnet(ctx context.Context, target Target) (*telnetConn, error) {
	dialer := &net.Dialer{Timeout: target.Timeout}

	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))

	if err != nil {
		return nil, err
	}

	return &telnetConn{Conn: conn}, nil
}

func (c *telnetConn) Read(p []byte) (int, error) {
	for {
		raw := make([]byte, len(p))
		n, err := c.Conn.Read(raw)

		data, replies := c.process(raw[:n])

		if len(replies) > 0 {
			if _, writeErr := c.Conn.Write(replies); writeErr != nil && err == nil {
				err = writeErr
			}
		}

		// A read made up only of negotiation is not passed on as an empty read, which callers take as no progress
		if len(data) > 0 || err != nil {
			return copy(p, data), err
		}
	}
}

// process separates the data in raw from the Telnet commands, returning the data and the replies to send
func (c *telnetConn) process(raw []byte) ([]byte, []byte) {
	data := make([]byte, 0, len(raw))
	replies := make([]byte, 0)

	for _, b := range raw {
		switch c.state {
		case telnetData:
			if b == telnetIAC {
				c.state = telnetCommand
			} else {
				data = append(data, b)
			}
		case telnetCommand:
			switch b {
			case telnetIAC:
				data = append(data, telnetIAC)
				c.state = telnetData
			case telnetWill, telnetWont, telnetDo, telnetDont:
				c.command = b
				c.state = telnetOption
			case telnetSB:
				c.state = telnetSubnegotiation
			default:
				c.state = telnetData
			}
		case telnetOption:
			replies = append(replies, telnetReply(c.command, b)...)
			c.state = telnetData
		case telnetSubnegotiation:
			if b == telnetIAC {
				c.state = telnetSubnegotiationIAC
			}
		case telnetSubnegotiationIAC:
			if b == telnetSE {
				c.state = telnetData
			} else {
				c.state = telnetSubnegotiation
			}
		}
	}

	return data, replies
}

func telnetReply(command, option byte) []byte {
	accepted := option == telnetOptionEcho || option == telnetOptionSuppressGoAhead

	switch {
	case command == telnetWill && accepted:
		return []byte{telnetIAC, telnetDo, option}
	case command == telnetWill:
		return []byte{telnetIAC, telnetDont, option}
	case command == telnetDo && option == telnetOptionSuppressGoAhead:
		return []byte{telnetIAC, telnetWill, option}
	case command == telnetDo:
		return []byte{telnetIAC, telnetWont, option}
	default:
		return nil
	}
}

// Write escapes bytes that would be read as Telnet commands
func (c *telnetConn) Write(p []byte) (int, error) {
	if _, err := c.Conn.Write(bytes.ReplaceAll(p, []byte{telnetIAC}, []byte{telnetIAC, telnetIAC})); err != nil {
		return 0, err
	}

	return len(p), nil
}
