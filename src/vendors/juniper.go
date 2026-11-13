package vendors

import (
	"cmp"
	"main/src/api"
	"main/src/cli"
	"main/src/optics"
	"main/src/snmp"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidJuniperDOMEntry           = "1.3.6.1.4.1.2636.3.60.1.1.1.1"  // jnxDomCurrentEntry, indexed by ifIndex
	oidJuniperDOMLaneEntry       = "1.3.6.1.4.1.2636.3.60.1.2.1.1"  // jnxDomCurrentLaneEntry, indexed by ifIndex and lane
	oidJuniperBGPPeerEntry       = "1.3.6.1.4.1.2636.5.1.1.2.1.1.1" // jnxBgpM2PeerEntry, indexed by routing instance, local and remote address
	oidJuniperBGPPeerTimingEntry = "1.3.6.1.4.1.2636.5.1.1.2.4.1.1" // jnxBgpM2PeerEventTimesEntry, which augments jnxBgpM2PeerEntry

	// jnxBgpM2PeerEntry columns, the jnxBgpM2PeerStatus of a session whose state machine is halted, and the
	// jnxBgpM2PeerEventTimesEntry column of the established time
	juniperBGPPeerIdentifier      = 1
	juniperBGPPeerState           = 2
	juniperBGPPeerStatus          = 3
	juniperBGPPeerLocalAddress    = 7
	juniperBGPPeerLocalAS         = 9
	juniperBGPPeerRemoteAS        = 13
	juniperBGPPeerStatusHalted    = 1
	juniperBGPPeerEstablishedTime = 1
)

var juniper = &Vendor{
	Name:              "Juniper",
	EnterpriseNumbers: []string{"2636"},
	Model:             juniperModel,
	Optics:            juniperOptics,
	BGP:               juniperBGP,
	ConfigPlatforms:   []*ConfigPlatform{juniperJunos},
}

// juniperJunos logs root in to the FreeBSD shell (a "%" prompt), so it starts the CLI first. The commit line changes on
// every commit, and the routing engine line ("{master:0}") is printed before every prompt of a virtual chassis.
var juniperJunos = &ConfigPlatform{
	ID:    "JUNIPER_JUNOS",
	Name:  "Juniper Junos",
	Clean: dropLines(`^## Last commit:`, `^\{\w+(:\d+)?\}$`),
	Profile: cli.Profile{
		Prompt: regexp.MustCompile(`^[\w.\-@:]+[>#%] ?$`),
		Commands: func(prompt string) []string {
			commands := []string{"set cli screen-length 0", "set cli screen-width 0", "show configuration | no-more"}

			if strings.HasSuffix(strings.TrimSpace(prompt), "%") {
				return append([]string{"cli"}, commands...)
			}

			return commands
		},
	},
}

// juniperModel reads a sysDescr such as "Juniper Networks, Inc. ex3300-48p Ethernet Switch, kernel JUNOS 15.1R7.9"
func juniperModel(description string) string {
	if fields := strings.Fields(description); len(fields) > 3 && strings.HasPrefix(description, "Juniper Networks, Inc. ") {
		return strings.ToUpper(fields[3])
	}

	return ""
}

func juniperOptics(client *gosnmp.GoSNMP, _ []api.Interface) (optics.Readings, error) {
	laneRows, err := snmp.WalkTable(client, oidJuniperDOMLaneEntry, 6, 7, 8)

	if err != nil {
		return nil, err
	}

	rows, err := snmp.WalkTable(client, oidJuniperDOMEntry, 5, 6, 7, 8, 9, 10, 17, 18, 25)

	if err != nil {
		return nil, err
	}

	return juniperReadings(rows, laneRows), nil
}

// juniperReadings reads JUNIPER-DOM-MIB, which reports levels in hundredths of a dBm, bias current in microamps,
// temperature in degrees Celsius and supply voltage in millivolts. Multi-lane optics (QSFP) are read per lane from the
// lane table, everything else from the per-interface table. Alarm thresholds, temperature and voltage only exist in
// the per-interface table and apply to the whole module.
func juniperReadings(rows, laneRows map[string]snmp.Row) optics.Readings {
	readings := make(optics.Readings)

	for index, row := range laneRows {
		rawInterface, rawLane, ok := strings.Cut(index, ".")
		interfaceIndex, interfaceErr := strconv.Atoi(rawInterface)
		lane, laneErr := strconv.Atoi(rawLane)

		if !ok || interfaceErr != nil || laneErr != nil {
			continue
		}

		if value, ok := row.Number(6); ok {
			readings.Add(interfaceIndex, lane, true, float64(value)/100)
		}

		if value, ok := row.Number(8); ok {
			readings.Add(interfaceIndex, lane, false, float64(value)/100)
		}

		if value, ok := row.Number(7); ok {
			readings.AddBiasCurrent(interfaceIndex, lane, float64(value)/1000)
		}
	}

	for index, row := range rows {
		interfaceIndex, err := strconv.Atoi(index)

		if err != nil {
			continue
		}

		if readings[interfaceIndex] == nil {
			if value, ok := row.Number(5); ok {
				readings.Add(interfaceIndex, 0, true, float64(value)/100)
			}

			if value, ok := row.Number(7); ok {
				readings.Add(interfaceIndex, 0, false, float64(value)/100)
			}

			if value, ok := row.Number(6); ok {
				readings.AddBiasCurrent(interfaceIndex, 0, float64(value)/1000)
			}
		}

		if value, ok := row.Number(8); ok {
			readings.SetTemperature(interfaceIndex, float64(value))
		}

		if value, ok := row.Number(25); ok {
			readings.SetVoltage(interfaceIndex, float64(value)/1000)
		}

		juniperThresholds(readings, interfaceIndex, true, row, 10, 9)
		juniperThresholds(readings, interfaceIndex, false, row, 18, 17)
	}

	return readings
}

// juniperThresholds reads the low and high alarm thresholds of one light level from their columns in jnxDomCurrentTable
// (receive power in 10 and 9, transmit power in 18 and 17)
func juniperThresholds(readings optics.Readings, interfaceIndex int, receive bool, row snmp.Row, lowColumn, highColumn int) {
	low, lowOK := row.Number(lowColumn)
	high, highOK := row.Number(highColumn)

	if lowOK && highOK {
		readings.SetThresholds(interfaceIndex, receive, float64(low)/100, float64(high)/100)
	}
}

// juniperBGP reads BGP4-V2-MIB-JUNIPER, which Junos fills for every routing instance and for IPv6 peers, unlike its
// BGP4-MIB
func juniperBGP(client *gosnmp.GoSNMP) (*api.BGP, error) {
	rows, err := snmp.WalkTable(client, oidJuniperBGPPeerEntry, juniperBGPPeerIdentifier, juniperBGPPeerState, juniperBGPPeerStatus, juniperBGPPeerLocalAddress, juniperBGPPeerLocalAS, juniperBGPPeerRemoteAS)

	if err != nil || len(rows) < 1 {
		return &api.BGP{Peers: make([]api.BGPPeer, 0)}, err
	}

	timingRows, err := snmp.WalkTable(client, oidJuniperBGPPeerTimingEntry, juniperBGPPeerEstablishedTime)

	if err != nil {
		return nil, err
	}

	return &api.BGP{Peers: juniperBGPPeers(rows, timingRows)}, nil
}

// juniperBGPPeers reads jnxBgpM2PeerTable rows, indexed by routing instance, then the local and remote address (each
// with its type, both of the same family), and the established time from the table augmenting it
func juniperBGPPeers(rows, timingRows map[string]snmp.Row) []api.BGPPeer {
	peers := make([]api.BGPPeer, 0, len(rows))

	for index, row := range rows {
		parts := strings.Split(index, ".")

		if len(parts) < 2 {
			continue
		}

		localAddress, rest, localOK := snmp.ShiftInetAddress(parts[1:], (len(parts)-1)/2)
		address, _, remoteOK := snmp.ShiftInetAddress(rest, 0)

		if !localOK || !remoteOK {
			continue
		}

		peer := api.BGPPeer{
			Address:      address,
			RouterID:     juniperBGPIdentifier(row.Bytes(juniperBGPPeerIdentifier)),
			LocalAddress: cmp.Or(snmp.FormatInetAddress(row.Bytes(juniperBGPPeerLocalAddress)), localAddress),
			RemoteAS:     row.Int(juniperBGPPeerRemoteAS),
			State:        api.BGPPeerStates[row.Int(juniperBGPPeerState)],
			AdminEnabled: row.Int(juniperBGPPeerStatus) != juniperBGPPeerStatusHalted,
		}

		if localAS, ok := row.Number(juniperBGPPeerLocalAS); ok && localAS > 0 {
			peer.LocalAS = &localAS
		}

		if seconds, ok := timingRows[index].Number(juniperBGPPeerEstablishedTime); ok {
			peer.EstablishedTimeSeconds = &seconds
		}

		peers = append(peers, peer)
	}

	return peers
}

// juniperBGPIdentifier writes a 4-octet BGP identifier in dotted form, leaving out 0.0.0.0 (not known yet)
func juniperBGPIdentifier(value []byte) string {
	if len(value) != 4 || (value[0]|value[1]|value[2]|value[3]) == 0 {
		return ""
	}

	return net.IP(value).String()
}
