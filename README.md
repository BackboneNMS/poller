# Backbone Polling Agent

The open source polling agent for [Backbone NMS](https://backbonenms.net). Deploy it anywhere inside your management network and it will monitor every device you add to the Backbone dashboard over ICMP and SNMP, and back up their configurations over SSH or Telnet. It needs no inbound ports and no database, and it reports to the Backbone polling servers rather than the dashboard, so monitoring keeps working while the dashboard is offline.

## How it works

1. Every polling interval, the poller asks a polling server for the devices assigned to it (`GET /api/poller/devices`), authenticated with the access token you generated on the dashboard's **Pollers** page. The interval itself is an organization setting on the dashboard, so it can be changed without touching the poller.
2. Each device is pinged (three echoes, for reachability and latency) and, unless it is ping-only, polled over SNMP v1, v2c or v3 with the credentials stored on the dashboard. A device can have several credentials to try (the organization's shared communities, for example): they are tried in the order the polling server sends them until one answers, and the poller reports which one worked so it is put first next time. Up to `snmp.concurrency` devices are polled at once. Devices that were down or unknown at their last poll (marked `priority` by the polling server) are started first so outages and recoveries show up sooner, and the rest take any free slots as soon as they open. Only the table columns the dashboard uses are requested, since some agents (Juniper EX switches especially) answer slowly per value, and a device that did not answer SNMP on its last poll is probed without retries. See [Request limits](#request-limits) for how the poller avoids overloading fragile agents.

   Every device gets a **status poll** each cycle: the system details, interface status and traffic counters, and CPU, memory and storage. A **full discovery** also walks the tables that change less often (IP addresses, LLDP neighbors, inventory, transceivers and the MAC address forwarding table). The polling server marks the devices due for discovery in the device list, and the poller discovers them in the background, up to `snmp.discovery_concurrency` at a time, with requested rediscoveries first, then devices never discovered, then the ones discovered longest ago. A device being discovered is left out of that cycle's status poll.
3. Results are pushed back to the polling server in batches (`POST /api/poller/results`), several at a time and gzip compressed when the server supports it. Uploads run alongside polling, so a slow upload does not hold up polling.
4. Devices whose configuration is due to be backed up come with a `configBackup` in the device list. The poller backs them up in the background, up to `config_backup.concurrency` at a time, by logging in over SSH or Telnet and running the platform's commands (see [Config backups](#config-backups)), and submits each configuration on its own (`POST /api/poller/configs`).

Several polling server URLs can be configured. The poller keeps using the one that answered last and fails over to the others, in order, when it cannot be reached or responds with a server error. Resubmitting a batch to another server after a timeout is safe, as the servers ignore results they already stored.

Only outbound HTTPS to the polling servers is required. Interface throughput is calculated on the poller from the difference in octet counters between polls, so the first poll after a restart reports counters without rates.

### Config backups

SNMP cannot read a device's configuration: the closest it gets is Cisco's CONFIG-COPY-MIB, which needs write access and pushes the file to a TFTP or SCP server, and Juniper's JUNIPER-CFGMGMT-MIB only says when the configuration changed. So the poller logs in to the device's command line instead, the way RANCID and Oxidized do, with the logins stored on the dashboard (the organization's shared logins in order until one is accepted, or the device's own).

The platform decides which commands turn off paging and print the configuration. It is chosen on the dashboard, or detected from the `sysObjectID` and `sysDescr` of the device's last SNMP discovery:

| Platform | Commands |
| --- | --- |
| Cisco IOS / IOS XE | `enable` if needed, `terminal length 0`, `show running-config` |
| Cisco NX-OS | `terminal length 0`, `show running-config` |
| Cisco IOS XR | `terminal length 0`, `show running-config` |
| Arista EOS | `enable` if needed, `terminal length 0`, `show running-config` |
| Juniper Junos | `cli` from root's shell, `set cli screen-length 0`, `show configuration \| no-more` |
| MikroTik RouterOS | `/export`, logging in as `user+ct511w4098h` to turn off colors and wrapping |
| Nokia SR OS | `environment no more`, `admin display-config` (classic CLI) or `environment more false`, `admin show configuration` (MD-CLI) |
| Nokia ISAM | `environment inhibit-alarms mode batch`, `info configure flat` |
| HPE ProCurve / ArubaOS-Switch | `enable` if needed, `no page`, `show running-config` |
| Ubiquiti EdgeOS / VyOS | `terminal length 0`, `show configuration commands \| cat` |
| TP-Link JetStream / Omada switch | `enable` if needed, `show running-config`, answering its "Press any key to continue" pager |

Lines that change on every read (build banners, byte counts, "last changed" timestamps, NTP clock-period) are removed so the polling server only stores a new revision when something really changed. Over SSH, the host key fingerprint is recorded at the first backup and the device must present it from then on: a different key fails the backup before any credentials are sent. Older key exchanges and ciphers are offered after the modern ones, since a lot of network equipment supports nothing newer. The platforms have been tested against simulated devices, not real hardware.

### Request limits

The SNMP agents of many switches compute each value on request, reading interface counters from the switching chip or searching the whole forwarding table for every lookup, and some freeze or reboot when one request asks for too much. TP-Link JetStream and Omada switches are known to. So the poller:

- names at most 10 objects in one request and asks for at most 25 rows per GetBulk (about 50 values in all), splitting larger reads into several requests;
- starts every table walk with a single row and doubles that with each answer, so a table the device does not have costs one tiny request rather than one the agent fills with whatever follows the table;
- asks for half as many rows when an agent answers `tooBig`;
- reads ifTable's 32-bit counters and speed only for interfaces without the 64-bit ones in ifXTable, which saves three columns per interface on most devices.

Vendors with fragile agents get stricter limits from the system group onwards. TP-Link devices are read one object per request, 10 rows at a time, with at least 20 ms between requests, the way net-snmp's `snmpbulkwalk` reads them, and their transceiver tables are read without GetBulk, as LibreNMS does.

When a discovery stops getting answers part way, the polling server holds back automatic rediscoveries of that device for an hour, doubling with each failure in a row up to the organization's rediscovery interval, so a device that crashed is not crashed again as soon as it comes back. Rediscoveries requested on the dashboard still run straight away.

### What is collected

| Data | MIB |
| --- | --- |
| Name, description, contact, location, uptime | SNMPv2-MIB `system` |
| Interfaces with status, speed, counters, errors and throughput | IF-MIB `ifTable` / `ifXTable` |
| IPv4 addresses per interface | IP-MIB `ipAddrTable` |
| Manufacturer, model, serial number and firmware | ENTITY-MIB, plus vendor detection from `sysObjectID`; TIMETRA-CHASSIS-MIB (Nokia SR OS), ASAM-EQUIP-MIB and ASAM-SYSTEM-MIB (Nokia ISAM), TPLINK-SYSINFO-MIB (TP-Link) |
| CPU load, memory and storage usage | HOST-RESOURCES-MIB; TIMETRA-SYSTEM-MIB (Nokia SR OS), ASAM-SYSTEM-MIB (Nokia ISAM) and TPLINK-SYSMONITOR-MIB (TP-Link, memory as a percentage only) for CPU and memory |
| Interface MAC addresses | IF-MIB `ifPhysAddress` |
| Directly connected neighbors | LLDP-MIB |
| Inventory (chassis, modules, ports, transceivers, power supplies, fans) and the interface each part belongs to | ENTITY-MIB `entPhysicalTable` and `entAliasMappingTable` |
| MAC addresses learned on each interface, by VLAN | Q-BRIDGE-MIB `dot1qTpFdbTable`, otherwise BRIDGE-MIB `dot1dTpFdbTable` (per VLAN context on Cisco, from CISCO-VTP-MIB) |
| Transceiver receive and transmit light levels, per lane | ENTITY-SENSOR-MIB, CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB, MIKROTIK-MIB, TIMETRA-PORT-MIB (Nokia SR OS), TPLINK-DDMSTATUS-MIB (TP-Link) |
| Transceiver low and high alarm thresholds for those light levels | CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB, TIMETRA-PORT-MIB (Nokia SR OS), TPLINK-DDMRXPOWTHRESHOLD-MIB and TPLINK-DDMTXPOWTHRESHOLD-MIB (TP-Link) |
| Transceiver temperature, supply voltage and laser bias current | ENTITY-SENSOR-MIB, CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB, MIKROTIK-MIB, TPLINK-DDMSTATUS-MIB (TP-Link) |
| Transceiver type, manufacturer, part number and serial number | ENTITY-MIB, MIKROTIK-MIB (also wavelength) |

Nokia SR OS routers and switches (7750 SR, 7250 IXR, 7210 SAS and others) and ISAM access nodes and OLTs (7302, 7330, 7360 FX) implement neither HOST-RESOURCES-MIB nor, on SR OS, the standard hardware and sensor MIBs, so their own MIBs are read instead when the standard ones come back empty. Model and software version on SR OS come from the `TiMOS-...` system description. TP-Link switches report their model as part of the hardware version (`SX3032F 1.0`), and their transceiver readings as text, with optical power in milliwatts. TP-Link support has been tested against a simulated device built from the TPLINK-* MIBs, not real hardware.

The forwarding table is read from the port column only, since walks cost time per value and the tables can hold thousands of addresses. Q-BRIDGE-MIB filtering databases are mapped to VLANs with `dot1qVlanFdbId`, or taken to be the VLAN ID when the device has no VLAN table. Cisco IOS serves BRIDGE-MIB one VLAN at a time, so each operational VLAN is read in its own context (`community@vlan` on SNMPv1 and v2c, context `vlan-N` on SNMPv3, which the SNMP group must be allowed to read). Addresses on the device's own ports (port 0) are left out. A discovery that fails to read the table leaves the previous one in place.

## Installation

Create a poller on the dashboard's **Pollers** page and copy its access token. The token is only shown once.

### Docker

```sh
docker run -d --name backbone-polling-agent --restart unless-stopped \
  -e POLLING_SERVER_URLS=https://poll.backbonenms.net \
  -e POLLING_SERVER_TOKEN=bbp_... \
  ghcr.io/backbonenms/polling-agent:latest
```

### From source

Requires Go 1.25 and the `ping` command (iputils on Linux).

```sh
make build
cp config.example.yml config.yml   # then set polling_server.token
./bin/main
```

To run it as a service, create a systemd unit that runs `bin/main` from the directory containing `config.yml`.

## Configuration

Settings are read from `config.yml` in the working directory, if it exists. Environment variables override the file.

| Key | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `polling_server.urls` | `POLLING_SERVER_URLS` (comma-separated) | | Base URLs of the polling servers, tried in order |
| `polling_server.token` | `POLLING_SERVER_TOKEN` | | Poller access token from the dashboard's **Pollers** page |
| `polling_server.timeout` | | `30s` | Timeout for each request to a polling server |
| `snmp.concurrency` | | `128` | Maximum number of devices polled at the same time. Polling is mostly waiting on the network, so this can go higher for large networks |
| `snmp.discovery_concurrency` | | `4` | Maximum number of devices fully discovered at the same time, in the background |
| `snmp.timeout` | | `5s` | Timeout for each SNMP request |
| `snmp.retries` | | `1` | Retries for each SNMP request |
| `config_backup.concurrency` | | `4` | Maximum number of devices backed up at the same time, in the background |
| `config_backup.timeout` | | `30s` | Timeout for connecting to a device over SSH or Telnet, and for each wait for it to respond |

Logs are written to stdout as JSON.

## Development

```sh
go vet ./...
go test ./...
make build
```

The data sent to the polling server is defined in [src/api/defs.go](src/api/defs.go), which must stay in sync with the polling server's copy. SNMP collection from the standard MIBs lives in [src/snmp.go](src/snmp.go), with the shared SNMP primitives in [src/snmp](src/snmp), ENTITY-MIB (physical entities and the interfaces they belong to) in [src/entity](src/entity), transceiver diagnostics in [src/optics](src/optics) and the forwarding table in [src/fdb.go](src/fdb.go).

### Adding a vendor

Everything vendor-specific lives in [src/vendors](src/vendors), one file per vendor, each defining a `Vendor` ([vendor.go](src/vendors/vendor.go)) and listed in `registry` there. A device's vendor is identified by the enterprise number in its `sysObjectID`, and each enterprise number can belong to only one vendor. Only `Name` and `EnterpriseNumbers` are required; the rest are optional hooks, which only fill in what the standard MIBs did not:

| Field | Called | Use |
| --- | --- | --- |
| `Model` | Discoveries, when ENTITY-MIB has no model | Read the model out of the `sysDescr` |
| `Hardware` | Discoveries | Serial number and versions from the vendor's own MIBs |
| `Resources` | Every poll, when HOST-RESOURCES-MIB lacks CPU or memory | CPU load and memory from the vendor's own MIBs |
| `Optics` | Discoveries | Transceiver light levels, other diagnostics and details from the vendor's own tables, into `optics.Readings` keyed by ifIndex |
| `SensorTables` | Discoveries, when `Optics` finds nothing | Variants of ENTITY-SENSOR-MIB, described as `optics.SensorTable`s, tried before the standard one |
| `ForwardingVLANs` | Discoveries, when Q-BRIDGE-MIB has no forwarding table | The VLANs whose BRIDGE-MIB forwarding tables are only served in a per-VLAN context |
| `ConfigPlatforms` | Config backups | The command lines the vendor's configurations are backed up from, as `ConfigPlatform`s ([configplatform.go](src/vendors/configplatform.go)): a prompt pattern, the commands to run, a `sysDescr` pattern for detection and a `Clean` for lines that change on every read. Their IDs must match the dashboard's `ConfigPlatform` enum |

Read tables with `snmp.WalkTable` (requesting only the columns used) and scalars with `snmp.GetRow`. To test a vendor against a simulated device, serve the values from a real device's `snmpwalk -On` with [snmptest](src/snmp/snmptest/agent.go): `snmptest.NewClient` returns a client to call the hooks with, as in [nokia_sros_test.go](src/vendors/nokia_sros_test.go). The SSH and Telnet session itself (logging in, `enable`, answering pagers) is in [src/cli](src/cli), tested against a simulated device in [fetch_test.go](src/cli/fetch_test.go).

## License

[MIT](LICENSE)
