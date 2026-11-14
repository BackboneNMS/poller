package main

import (
	"context"
	"main/src/api"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

const (
	testOIDLinkDown       = "1.3.6.1.6.3.1.1.5.3"
	testOIDIfIndex        = "1.3.6.1.2.1.2.2.1.1.12"
	testOIDIfAdminStatus  = "1.3.6.1.2.1.2.2.1.7.12"
	testOIDIfOperStatus   = "1.3.6.1.2.1.2.2.1.8.12"
	testOIDIfName         = "1.3.6.1.2.1.31.1.1.1.1.12"
	testTrapEngineID      = "\x80\x00\x1f\x88\x04device"
	testTrapUsername      = "backbone"
	testTrapAuthPassword  = "authentication"
	testTrapPrivacyPasswd = "privacy-password"
)

func linkDownVariables() []gosnmp.SnmpPDU {
	return []gosnmp.SnmpPDU{
		{Name: "." + oidSystemUptime, Type: gosnmp.TimeTicks, Value: uint32(123456)},
		{Name: "." + oidSNMPTrapOID, Type: gosnmp.ObjectIdentifier, Value: "." + testOIDLinkDown},
		{Name: "." + testOIDIfIndex, Type: gosnmp.Integer, Value: 12},
		{Name: "." + testOIDIfAdminStatus, Type: gosnmp.Integer, Value: 1},
		{Name: "." + testOIDIfOperStatus, Type: gosnmp.Integer, Value: 2},
		{Name: "." + testOIDIfName, Type: gosnmp.OctetString, Value: []byte("ge-0/0/11")},
	}
}

func TestMessageVersion(t *testing.T) {
	for _, version := range []gosnmp.SnmpVersion{gosnmp.Version1, gosnmp.Version2c} {
		packet := &gosnmp.SnmpPacket{Version: version, Community: "public", PDUType: gosnmp.SNMPv2Trap, Variables: linkDownVariables()}

		if version == gosnmp.Version1 {
			packet.PDUType = gosnmp.Trap
			packet.SnmpTrap = gosnmp.SnmpTrap{Enterprise: ".1.3.6.1.4.1.2636", AgentAddress: "10.0.0.1", GenericTrap: 2}
		}

		message, err := packet.MarshalMsg()

		if err != nil {
			t.Fatal(err)
		}

		if got, ok := messageVersion(message); !ok || got != version {
			t.Fatalf("expected version %v, got %v (%v)", version, got, ok)
		}
	}

	// A message over 127 bytes has a long-form length
	long := append([]byte{0x30, 0x81, 200, 0x02, 0x01, 0x03}, make([]byte, 200)...)

	if got, ok := messageVersion(long); !ok || got != gosnmp.Version3 {
		t.Fatalf("expected version 3 from a long-form length, got %v (%v)", got, ok)
	}

	for _, message := range [][]byte{nil, {0x30}, []byte("GET / HTTP/1.1"), {0x30, 0x03, 0x02, 0x01, 0x02}} {
		if _, ok := messageVersion(message); ok {
			t.Fatalf("expected %v to be rejected", message)
		}
	}
}

func TestV1TrapOID(t *testing.T) {
	cases := []struct {
		enterprise        string
		generic, specific int
		want              string
	}{
		{"1.3.6.1.4.1.9", 0, 0, "1.3.6.1.6.3.1.1.5.1"},
		{"1.3.6.1.4.1.9", 2, 0, "1.3.6.1.6.3.1.1.5.3"},
		{"1.3.6.1.4.1.9", 3, 0, "1.3.6.1.6.3.1.1.5.4"},
		{"1.3.6.1.4.1.2636.4.1", 6, 7, "1.3.6.1.4.1.2636.4.1.0.7"},
		{"", 6, 7, ""},
		{"1.3.6.1.4.1.9", 7, 0, ""},
	}

	for _, c := range cases {
		if got := v1TrapOID(c.enterprise, c.generic, c.specific); got != c.want {
			t.Fatalf("v1TrapOID(%q, %d, %d) = %q, expected %q", c.enterprise, c.generic, c.specific, got, c.want)
		}
	}
}

func TestBuildTrapSenders(t *testing.T) {
	devices := []api.Device{
		{ID: "router", Host: "10.0.0.1", Addresses: []string{"192.0.2.1", "198.51.100.1", "127.0.0.1", "fe80::1"}},
		{ID: "switch", Host: "switch.example.net", Addresses: []string{"198.51.100.1", "10.0.0.1"}},
		{ID: "copy-a", Host: "10.0.0.9"},
		{ID: "copy-b", Host: "10.0.0.9"},
		{ID: "v6", Host: "2001:db8::1"},
	}

	senders := buildTrapSenders(devices, map[string][]netip.Addr{"switch.example.net": {netip.MustParseAddr("10.0.0.2")}})

	expect := map[string]string{
		"10.0.0.1":    "router",
		"192.0.2.1":   "router",
		"10.0.0.2":    "switch",
		"2001:db8::1": "v6",
	}

	for address, deviceID := range expect {
		if device, ok := senders[netip.MustParseAddr(address)]; !ok || device.ID != deviceID {
			t.Fatalf("expected %s to be matched to %s, got %+v", address, deviceID, device)
		}
	}

	// Shared by two devices' interfaces, two devices' hosts, loopback and link-local
	for _, address := range []string{"198.51.100.1", "10.0.0.9", "127.0.0.1", "fe80::1"} {
		if device, ok := senders[netip.MustParseAddr(address)]; ok {
			t.Fatalf("expected %s to be left out, got %s", address, device.ID)
		}
	}
}

// receiveTraps starts a trap receiver on a local port that knows one device at 127.0.0.1, and returns it with the port
func receiveTraps(t *testing.T, device api.Device) (*trapReceiver, uint16) {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})

	if err != nil {
		t.Fatal(err)
	}

	receiver := newTrapReceiver()
	senders := buildTrapSenders([]api.Device{device}, nil)

	receiver.senders.Store(&senders)

	ctx, cancel := context.WithCancel(context.Background())

	t.Cleanup(cancel)

	go receiver.listen(ctx, conn)

	return receiver, uint16(conn.LocalAddr().(*net.UDPAddr).Port)
}

// waitForTraps waits for the receiver to queue count traps and returns them
func waitForTraps(t *testing.T, receiver *trapReceiver, count int) []api.Trap {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		receiver.mu.Lock()
		pending := append([]api.Trap(nil), receiver.pending...)
		receiver.mu.Unlock()

		if len(pending) >= count {
			return pending
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected %d traps to be queued", count)

	return nil
}

func sendTrap(t *testing.T, client *gosnmp.GoSNMP, trap gosnmp.SnmpTrap) {
	t.Helper()

	client.Target = "127.0.0.1"
	client.Timeout = 2 * time.Second

	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}

	defer client.Conn.Close()

	if _, err := client.SendTrap(trap); err != nil {
		t.Fatal(err)
	}
}

func assertLinkDown(t *testing.T, trap api.Trap) {
	t.Helper()

	if trap.DeviceID != "router" || trap.TrapOID != testOIDLinkDown || trap.ReceivedAt.IsZero() {
		t.Fatalf("expected a linkDown from the router, got %+v", trap)
	}

	expect := []api.TrapVariable{
		{OID: testOIDIfIndex, Type: api.TrapVariableTypeInteger, Value: "12"},
		{OID: testOIDIfAdminStatus, Type: api.TrapVariableTypeInteger, Value: "1"},
		{OID: testOIDIfOperStatus, Type: api.TrapVariableTypeInteger, Value: "2"},
		{OID: testOIDIfName, Type: api.TrapVariableTypeString, Value: "ge-0/0/11"},
	}

	if len(trap.Variables) != len(expect) {
		t.Fatalf("expected the variables without sysUpTime and snmpTrapOID, got %+v", trap.Variables)
	}

	for i, variable := range expect {
		if trap.Variables[i] != variable {
			t.Fatalf("expected variable %d to be %+v, got %+v", i, variable, trap.Variables[i])
		}
	}
}

func TestReceiveTrapV2c(t *testing.T) {
	receiver, port := receiveTraps(t, api.Device{ID: "router", Host: "127.0.0.1"})

	sendTrap(t, &gosnmp.GoSNMP{Port: port, Version: gosnmp.Version2c, Community: "any-community"}, gosnmp.SnmpTrap{Variables: linkDownVariables()})

	assertLinkDown(t, waitForTraps(t, receiver, 1)[0])
}

func TestReceiveTrapV1(t *testing.T) {
	receiver, port := receiveTraps(t, api.Device{ID: "router", Host: "127.0.0.1"})

	sendTrap(t, &gosnmp.GoSNMP{Port: port, Version: gosnmp.Version1, Community: "public"}, gosnmp.SnmpTrap{
		Enterprise:   ".1.3.6.1.4.1.2636",
		AgentAddress: "127.0.0.1",
		GenericTrap:  2,
		Variables:    linkDownVariables()[2:],
	})

	assertLinkDown(t, waitForTraps(t, receiver, 1)[0])
}

// An inform is acknowledged, so SendTrap returns only once the receiver's response arrives
func TestReceiveInform(t *testing.T) {
	receiver, port := receiveTraps(t, api.Device{ID: "router", Host: "127.0.0.1"})

	sendTrap(t, &gosnmp.GoSNMP{Port: port, Version: gosnmp.Version2c, Community: "public", Retries: 0}, gosnmp.SnmpTrap{Variables: linkDownVariables(), IsInform: true})

	assertLinkDown(t, waitForTraps(t, receiver, 1)[0])
}

func TestReceiveTrapV3(t *testing.T) {
	credential := api.SNMPCredential{
		Version:         api.SNMPVersion3,
		Username:        testTrapUsername,
		AuthProtocol:    "SHA",
		AuthPassword:    testTrapAuthPassword,
		PrivacyProtocol: "AES",
		PrivacyPassword: testTrapPrivacyPasswd,
	}

	device := api.Device{ID: "router", Host: "127.0.0.1", SNMP: api.DeviceSNMP{Credentials: []api.SNMPCredential{
		{Version: api.SNMPVersion2C, Community: "public"},
		{Version: api.SNMPVersion3, Username: testTrapUsername, AuthProtocol: "SHA", AuthPassword: "wrong-password"},
		credential,
	}}}

	receiver, port := receiveTraps(t, device)

	send := func(authPassword string) {
		sendTrap(t, &gosnmp.GoSNMP{
			Port:          port,
			Version:       gosnmp.Version3,
			SecurityModel: gosnmp.UserSecurityModel,
			MsgFlags:      gosnmp.AuthPriv,
			SecurityParameters: &gosnmp.UsmSecurityParameters{
				UserName:                 testTrapUsername,
				AuthoritativeEngineID:    testTrapEngineID,
				AuthoritativeEngineBoots: 1,
				AuthoritativeEngineTime:  100,
				AuthenticationProtocol:   gosnmp.SHA,
				AuthenticationPassphrase: authPassword,
				PrivacyProtocol:          gosnmp.AES,
				PrivacyPassphrase:        testTrapPrivacyPasswd,
			},
		}, gosnmp.SnmpTrap{Variables: linkDownVariables()})
	}

	// None of the device's credentials authenticate the first, so only the second is queued
	send("not-the-password")
	send(testTrapAuthPassword)

	queued := waitForTraps(t, receiver, 1)

	time.Sleep(100 * time.Millisecond)

	if queued = waitForTraps(t, receiver, 1); len(queued) != 1 {
		t.Fatalf("expected only the authenticated trap to be queued, got %d", len(queued))
	}

	assertLinkDown(t, queued[0])
}

func TestReceiveTrapFromUnknownAddress(t *testing.T) {
	receiver, port := receiveTraps(t, api.Device{ID: "router", Host: "10.0.0.1"})

	sendTrap(t, &gosnmp.GoSNMP{Port: port, Version: gosnmp.Version2c, Community: "public"}, gosnmp.SnmpTrap{Variables: linkDownVariables()})

	// An SNMPv1 trap relayed from an unknown address is matched by the agent address inside it
	sendTrap(t, &gosnmp.GoSNMP{Port: port, Version: gosnmp.Version1, Community: "public"}, gosnmp.SnmpTrap{
		Enterprise:   ".1.3.6.1.4.1.2636",
		AgentAddress: "10.0.0.1",
		GenericTrap:  2,
		Variables:    linkDownVariables()[2:],
	})

	queued := waitForTraps(t, receiver, 1)

	time.Sleep(100 * time.Millisecond)

	if queued = waitForTraps(t, receiver, 1); len(queued) != 1 || queued[0].DeviceID != "router" || queued[0].TrapOID != testOIDLinkDown {
		t.Fatalf("expected only the relayed SNMPv1 trap to be queued, got %+v", queued)
	}
}

func TestTrapQueue(t *testing.T) {
	receiver := newTrapReceiver()
	now := time.Now()

	receiver.enqueue(api.Trap{DeviceID: "expired", ReceivedAt: now.Add(-maxTrapAge - time.Second)})

	for i := range 3 {
		receiver.enqueue(api.Trap{DeviceID: "fresh", ReceivedAt: now.Add(time.Duration(i) * time.Second)})
	}

	batch, expired := receiver.take(2, now)

	if expired != 1 || len(batch) != 2 || batch[0].ReceivedAt != now {
		t.Fatalf("expected the expired trap dropped and the two oldest fresh ones taken, got %d expired and %+v", expired, batch)
	}

	receiver.requeue(batch)

	if batch, _ = receiver.take(maxTrapBatch, now); len(batch) != 3 || batch[0].ReceivedAt != now || batch[2].ReceivedAt != now.Add(2*time.Second) {
		t.Fatalf("expected requeued traps back in front, in order, got %+v", batch)
	}

	for range maxPendingTraps + 5 {
		receiver.enqueue(api.Trap{ReceivedAt: now})
	}

	if len(receiver.pending) != maxPendingTraps {
		t.Fatalf("expected the queue capped at %d, got %d", maxPendingTraps, len(receiver.pending))
	}
}

func TestTrapVariableHexString(t *testing.T) {
	variable, ok := trapVariable("1.3.6.1.2.1.2.2.1.6.12", gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte{0x00, 0x1b, 0x21, 0x3c, 0x9d, 0xf8}})

	if !ok || variable.Type != api.TrapVariableTypeHexString || variable.Value != "00:1B:21:3C:9D:F8" {
		t.Fatalf("expected binary octets as hex, got %+v", variable)
	}

	if _, ok = trapVariable("1.3.6.1.2.1.1.1.0", gosnmp.SnmpPDU{Type: gosnmp.Null}); ok {
		t.Fatal("expected NULL to be skipped")
	}
}
