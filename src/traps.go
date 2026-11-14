package main

import (
	"context"
	"errors"
	"main/src/api"
	"main/src/snmp"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gosnmp/gosnmp"
)

const (
	oidSNMPTrapOID   = "1.3.6.1.6.3.1.1.4.1.0"
	oidStandardTraps = "1.3.6.1.6.3.1.1.5"

	// v1EnterpriseSpecific is the generic-trap number of SNMPv1 traps that a specific-trap number identifies instead
	v1EnterpriseSpecific = 6

	// maxTrapMessageBytes is the largest UDP payload, so no trap is cut short
	maxTrapMessageBytes = 65535
	maxTrapVariables    = 64
	maxTrapValueLength  = 1024

	// trapGatherDelay is how long the first trap of a burst waits for the rest (an outage taking many links down at
	// once, for example), so they reach the polling server in one request
	trapGatherDelay = 2 * time.Second
	trapRetryDelay  = 15 * time.Second
	maxTrapBatch    = 500

	// maxPendingTraps and maxTrapAge bound the traps kept while no polling server can be reached. Older traps are
	// dropped, since the poll cycles have caught up with what they reported by then.
	maxPendingTraps = 10000
	maxTrapAge      = 10 * time.Minute

	trapWarningInterval = time.Hour
	maxTrapWarningKeys  = 1000
	hostLookupTimeout   = 5 * time.Second
	hostLookupWorkers   = 16
)

// traps receives the SNMP traps that devices send to the poller and forwards them to the polling server
var traps = newTrapReceiver()

type trapReceiver struct {
	// senders maps every address a trap may come from to the device that sends it
	senders  atomic.Pointer[map[netip.Addr]api.Device]
	updating atomic.Bool

	// resolved holds the addresses each host name last resolved to. Only the update in progress uses it.
	resolved map[string][]netip.Addr

	mu      sync.Mutex
	pending []api.Trap
	wake    chan struct{}

	warningsMu sync.Mutex
	warnings   map[string]time.Time
}

func newTrapReceiver() *trapReceiver {
	return &trapReceiver{
		resolved: make(map[string][]netip.Addr),
		wake:     make(chan struct{}, 1),
		warnings: make(map[string]time.Time),
	}
}

// RunTrapReceiver listens for SNMP traps on traps.port until ctx is done, forwarding them to the polling server. When
// the port cannot be opened (port 162 needs root or CAP_NET_BIND_SERVICE, and another trap receiver may hold it), the
// error is logged and the poller keeps polling without traps.
func RunTrapReceiver(ctx context.Context) {
	if !conf.Traps.Enabled {
		return
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: int(conf.Traps.Port)})

	if err != nil {
		logger.Error("Failed to listen for SNMP traps, traps will not be received", "port", conf.Traps.Port, "error", err)

		return
	}

	logger.Info("Listening for SNMP traps", "port", conf.Traps.Port)

	go traps.forward(ctx)

	traps.listen(ctx, conn)
}

// SetDevices matches traps against a newly fetched device list. Host names are looked up in the background so a slow
// DNS server never holds up polling, and the previous list stays in use until that finishes. A list that arrives while
// the previous one is still being looked up is skipped, since the next poll cycle brings another.
func (r *trapReceiver) SetDevices(ctx context.Context, devices []api.Device) {
	if !conf.Traps.Enabled || !r.updating.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer r.updating.Store(false)

		RunRecovered("UpdateTrapSenders", func() {
			senders := buildTrapSenders(devices, r.resolveHosts(ctx, devices))

			r.senders.Store(&senders)
		})
	}()
}

// listen reads traps from conn until ctx is done, then closes it
func (r *trapReceiver) listen(ctx context.Context, conn *net.UDPConn) {
	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { conn.Close() })

	defer stop()

	buffer := make([]byte, maxTrapMessageBytes)

	for {
		size, remote, err := conn.ReadFromUDPAddrPort(buffer)

		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}

			logger.Warn("Failed to read an SNMP trap", "error", err)

			continue
		}

		RunRecovered("ReceiveTrap", func() { r.receive(conn, buffer[:size], remote, time.Now()) })
	}
}

// receive handles one message: it decodes the trap (with the sending device's credentials for SNMPv3), acknowledges it
// when it is an inform, and queues it for the polling server. Traps from addresses that are not the poller's devices,
// and SNMPv3 traps none of the device's credentials authenticate, are dropped with a warning. The message is only
// valid until receive returns.
func (r *trapReceiver) receive(conn *net.UDPConn, message []byte, remote netip.AddrPort, receivedAt time.Time) {
	address := remote.Addr().Unmap().WithZone("")
	device, known := r.sender(address)
	version, ok := messageVersion(message)

	if !ok {
		r.warn("malformed/"+address.String(), "Received a message on the trap port that is not SNMP", "address", address.String())

		return
	}

	var packet *gosnmp.SnmpPacket

	if version == gosnmp.Version3 {
		if !known {
			r.warnUnknownSender(address)

			return
		}

		if packet = decodeTrapV3(message, device); packet == nil {
			r.warn("authentication/"+device.ID, "Could not authenticate an SNMPv3 trap with any of the device's SNMP credentials", "device", device.Name, "address", address.String())

			return
		}

		// The sender of an inform expects the receiver to be the authoritative engine, which the poller is not
		if packet.PDUType == gosnmp.InformRequest {
			r.warn("inform/"+device.ID, "Received an SNMPv3 inform, which is not supported; send SNMPv3 traps instead", "device", device.Name, "address", address.String())

			return
		}
	} else {
		var err error

		if packet, err = (&gosnmp.GoSNMP{}).UnmarshalTrap(message, false); err != nil {
			r.warn("malformed/"+address.String(), "Received a malformed SNMP trap", "address", address.String(), "error", err)

			return
		}

		// An SNMPv1 trap relayed by another host still names the device that raised it
		if !known && packet.PDUType == gosnmp.Trap {
			if agent, err := netip.ParseAddr(packet.AgentAddress); err == nil {
				device, known = r.sender(agent.Unmap().WithZone(""))
			}
		}

		if !known {
			r.warnUnknownSender(address)

			return
		}
	}

	trap, ok := trapFromPacket(device.ID, receivedAt, packet)

	if !ok {
		return
	}

	if packet.PDUType == gosnmp.InformRequest {
		if err := acknowledgeInform(conn, packet, remote); err != nil {
			logger.Warn("Failed to acknowledge an SNMP inform", "device", device.Name, "address", address.String(), "error", err)
		}
	}

	r.enqueue(trap)
}

func (r *trapReceiver) sender(address netip.Addr) (api.Device, bool) {
	senders := r.senders.Load()

	if senders == nil {
		return api.Device{}, false
	}

	device, ok := (*senders)[address]

	return device, ok
}

func (r *trapReceiver) warnUnknownSender(address netip.Addr) {
	r.warn("unknown/"+address.String(), "Received an SNMP trap from an address that is not one of this poller's devices", "address", address.String())
}

// warn logs a warning at most once per trapWarningInterval for each key, so a device that keeps sending traps the
// poller cannot use does not flood the log
func (r *trapReceiver) warn(key, message string, args ...any) {
	now := time.Now()

	r.warningsMu.Lock()

	if last, ok := r.warnings[key]; ok && now.Sub(last) < trapWarningInterval {
		r.warningsMu.Unlock()

		return
	}

	if len(r.warnings) >= maxTrapWarningKeys {
		for other, last := range r.warnings {
			if now.Sub(last) >= trapWarningInterval {
				delete(r.warnings, other)
			}
		}
	}

	r.warnings[key] = now
	r.warningsMu.Unlock()

	logger.Warn(message, args...)
}

// enqueue queues a trap for the polling server, dropping the oldest when too many are waiting (no polling server has
// answered for a while during a trap storm)
func (r *trapReceiver) enqueue(trap api.Trap) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.pending) >= maxPendingTraps {
		r.pending = r.pending[1:]
	}

	r.pending = append(r.pending, trap)

	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// take removes up to limit traps from the front of the queue, first discarding the ones received longer than maxTrapAge
// before now, and returns how many it discarded
func (r *trapReceiver) take(limit int, now time.Time) ([]api.Trap, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := len(r.pending)

	r.pending = slices.DeleteFunc(r.pending, func(trap api.Trap) bool { return now.Sub(trap.ReceivedAt) > maxTrapAge })

	expired := count - len(r.pending)
	batch := slices.Clone(r.pending[:min(limit, len(r.pending))])

	r.pending = r.pending[len(batch):]

	return batch, expired
}

// requeue puts traps that could not be sent back at the front of the queue
func (r *trapReceiver) requeue(batch []api.Trap) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pending = append(slices.Clone(batch), r.pending...)

	if over := len(r.pending) - maxPendingTraps; over > 0 {
		r.pending = r.pending[over:]
	}
}

// forward sends queued traps to the polling server until ctx is done. Traps no polling server could take are retried
// until they are maxTrapAge old, while traps a polling server refused (one too old to know about traps, for example)
// are dropped, since every attempt would be refused the same way.
func (r *trapReceiver) forward(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(trapGatherDelay):
		}

		for {
			batch, expired := r.take(maxTrapBatch, time.Now())

			if expired > 0 {
				logger.Warn("Dropped SNMP traps that could not be forwarded in time", "count", expired)
			}

			if len(batch) < 1 {
				break
			}

			err := client.SubmitTraps(ctx, batch)

			if err == nil {
				continue
			}

			if ctx.Err() != nil {
				return
			}

			if api.IsRejected(err) {
				logger.Error("Polling server refused SNMP traps, dropping them", "count", len(batch), "error", err)

				continue
			}

			logger.Error("Failed to forward SNMP traps to polling server, retrying", "count", len(batch), "error", err)
			r.requeue(batch)

			select {
			case <-ctx.Done():
				return
			case <-time.After(trapRetryDelay):
			}
		}
	}
}

// resolveHosts looks up the addresses of the devices whose host is a name rather than an address. A name that fails to
// resolve keeps the addresses it last resolved to.
func (r *trapReceiver) resolveHosts(ctx context.Context, devices []api.Device) map[string][]netip.Addr {
	names := make([]string, 0)

	for _, device := range devices {
		if _, err := netip.ParseAddr(device.Host); err != nil && !slices.Contains(names, device.Host) {
			names = append(names, device.Host)
		}
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		slots    = make(chan struct{}, hostLookupWorkers)
		resolved = make(map[string][]netip.Addr, len(names))
	)

	for _, name := range names {
		slots <- struct{}{}

		wg.Go(func() {
			defer func() { <-slots }()

			lookupCtx, cancel := context.WithTimeout(ctx, hostLookupTimeout)

			defer cancel()

			addresses, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", name)

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				resolved[name] = addresses
			} else if previous, ok := r.resolved[name]; ok {
				resolved[name] = previous
			}
		})
	}

	wg.Wait()

	r.resolved = resolved

	return resolved
}

// buildTrapSenders maps the addresses traps may come from to the devices sending them: each device's host, and the
// other addresses its last discovery found, for devices that send traps from another interface. An address several
// devices claim is left out, since a trap from it could be from any of them, and a device's host always wins over
// another device's interface address.
func buildTrapSenders(devices []api.Device, resolved map[string][]netip.Addr) map[netip.Addr]api.Device {
	hosts := make(map[netip.Addr][]int)
	interfaces := make(map[netip.Addr][]int)

	for i, device := range devices {
		addresses := resolved[device.Host]

		if address, err := netip.ParseAddr(device.Host); err == nil {
			addresses = []netip.Addr{address}
		}

		for _, address := range addresses {
			address = address.Unmap().WithZone("")
			hosts[address] = appendUnique(hosts[address], i)
		}

		for _, value := range device.Addresses {
			address, err := netip.ParseAddr(value)

			if err != nil || address.IsLoopback() || address.IsUnspecified() || address.IsMulticast() || address.IsLinkLocalUnicast() {
				continue
			}

			address = address.Unmap().WithZone("")
			interfaces[address] = appendUnique(interfaces[address], i)
		}
	}

	senders := make(map[netip.Addr]api.Device, len(hosts)+len(interfaces))

	for address, owners := range interfaces {
		if _, isHost := hosts[address]; !isHost && len(owners) == 1 {
			senders[address] = devices[owners[0]]
		}
	}

	for address, owners := range hosts {
		if len(owners) == 1 {
			senders[address] = devices[owners[0]]
		}
	}

	return senders
}

func appendUnique[T comparable](values []T, value T) []T {
	if slices.Contains(values, value) {
		return values
	}

	return append(values, value)
}

// messageVersion reads the SNMP version at the start of a message (a SEQUENCE whose first element is the version), so
// SNMPv3 messages can be decoded with the sending device's credentials
func messageVersion(message []byte) (gosnmp.SnmpVersion, bool) {
	if len(message) < 2 || message[0] != 0x30 {
		return 0, false
	}

	cursor := 2

	// Lengths of 128 bytes or more take the long form: the number of length bytes that follow, then the length
	if message[1]&0x80 != 0 {
		cursor += int(message[1] & 0x7f)
	}

	if len(message) < cursor+3 || message[cursor] != 0x02 || message[cursor+1] != 1 {
		return 0, false
	}

	switch version := gosnmp.SnmpVersion(message[cursor+2]); version {
	case gosnmp.Version1, gosnmp.Version2c, gosnmp.Version3:
		return version, true
	default:
		return 0, false
	}
}

// decodeTrapV3 decodes an SNMPv3 trap with the first of the device's SNMPv3 credentials that authenticates and
// decrypts it. The device sending a trap is the authoritative engine, so the keys are localized to the engine ID in
// the message. It returns nil when none of them work.
func decodeTrapV3(message []byte, device api.Device) *gosnmp.SnmpPacket {
	for _, credential := range device.SNMP.Credentials {
		if credential.Version != api.SNMPVersion3 {
			continue
		}

		flags, parameters, err := snmpSecurityParameters(credential)

		if err != nil {
			continue
		}

		decoder := &gosnmp.GoSNMP{
			Version:            gosnmp.Version3,
			SecurityModel:      gosnmp.UserSecurityModel,
			MsgFlags:           flags,
			SecurityParameters: parameters,
		}

		// Decryption happens in place, so each attempt gets its own copy of the message
		if packet, err := decoder.UnmarshalTrap(slices.Clone(message), true); err == nil {
			return packet
		}
	}

	return nil
}

// acknowledgeInform answers an inform with the response its sender waits for: the same variable bindings in a
// Response-PDU (RFC 3416 section 4.2.7), so it stops sending it again
func acknowledgeInform(conn *net.UDPConn, packet *gosnmp.SnmpPacket, remote netip.AddrPort) error {
	response := *packet
	response.PDUType = gosnmp.GetResponse
	response.Error = gosnmp.NoError
	response.ErrorIndex = 0

	message, err := response.MarshalMsg()

	if err != nil {
		return err
	}

	_, err = conn.WriteToUDPAddrPort(message, remote)

	return err
}

// trapFromPacket converts a decoded notification into what the polling server receives. It reports false for messages
// that are not notifications.
func trapFromPacket(deviceID string, receivedAt time.Time, packet *gosnmp.SnmpPacket) (api.Trap, bool) {
	trap := api.Trap{
		DeviceID:   deviceID,
		ReceivedAt: receivedAt,
		Variables:  make([]api.TrapVariable, 0, min(len(packet.Variables), maxTrapVariables)),
	}

	switch packet.PDUType {
	case gosnmp.Trap:
		trap.TrapOID = v1TrapOID(strings.TrimPrefix(packet.Enterprise, "."), packet.GenericTrap, packet.SpecificTrap)
	case gosnmp.SNMPv2Trap, gosnmp.InformRequest:
	default:
		return api.Trap{}, false
	}

	for _, pdu := range packet.Variables {
		oid := strings.TrimPrefix(pdu.Name, ".")

		switch {
		case oid == oidSystemUptime:
		case oid == oidSNMPTrapOID:
			if packet.PDUType != gosnmp.Trap {
				trap.TrapOID = snmp.PDUString(pdu)
			}
		case len(trap.Variables) < maxTrapVariables:
			if variable, ok := trapVariable(oid, pdu); ok {
				trap.Variables = append(trap.Variables, variable)
			}
		}
	}

	return trap, len(trap.TrapOID) > 0
}

// v1TrapOID works out the snmpTrapOID of an SNMPv1 trap (RFC 3584 section 3.1): one of the standard traps under
// snmpTraps for generic traps 0 to 5 (coldStart to egpNeighborLoss), or the enterprise followed by 0 and the specific
// trap number for an enterprise-specific trap. It returns "" when neither applies.
func v1TrapOID(enterprise string, generic, specific int) string {
	switch {
	case generic >= 0 && generic < v1EnterpriseSpecific:
		return oidStandardTraps + "." + strconv.Itoa(generic+1)
	case generic == v1EnterpriseSpecific && len(enterprise) > 0:
		return enterprise + ".0." + strconv.Itoa(specific)
	default:
		return ""
	}
}

// trapVariable converts a variable binding into its wire form, reporting false for types that carry no value (NULL and
// the exceptions) or that devices do not send in traps
func trapVariable(oid string, pdu gosnmp.SnmpPDU) (api.TrapVariable, bool) {
	variable := api.TrapVariable{OID: oid}

	switch pdu.Type {
	case gosnmp.Integer:
		variable.Type, variable.Value = api.TrapVariableTypeInteger, gosnmp.ToBigInt(pdu.Value).String()
	case gosnmp.Counter32:
		variable.Type, variable.Value = api.TrapVariableTypeCounter32, gosnmp.ToBigInt(pdu.Value).String()
	case gosnmp.Gauge32, gosnmp.Uinteger32:
		variable.Type, variable.Value = api.TrapVariableTypeGauge32, gosnmp.ToBigInt(pdu.Value).String()
	case gosnmp.TimeTicks:
		variable.Type, variable.Value = api.TrapVariableTypeTimeTicks, gosnmp.ToBigInt(pdu.Value).String()
	case gosnmp.Counter64:
		variable.Type, variable.Value = api.TrapVariableTypeCounter64, gosnmp.ToBigInt(pdu.Value).String()
	case gosnmp.ObjectIdentifier:
		variable.Type, variable.Value = api.TrapVariableTypeOID, snmp.PDUString(pdu)
	case gosnmp.IPAddress:
		variable.Type, variable.Value = api.TrapVariableTypeIPAddress, snmp.PDUString(pdu)
	case gosnmp.OctetString:
		value, _ := pdu.Value.([]byte)

		if text, ok := printableText(value); ok {
			variable.Type, variable.Value = api.TrapVariableTypeString, strings.ToValidUTF8(TruncateString(text, maxTrapValueLength), "")
		} else {
			// Each octet takes three characters ("FF:"), so this keeps the value within maxTrapValueLength
			variable.Type, variable.Value = api.TrapVariableTypeHexString, snmp.FormatHexOctets(value[:min(len(value), maxTrapValueLength/3)])
		}
	default:
		return variable, false
	}

	return variable, true
}
