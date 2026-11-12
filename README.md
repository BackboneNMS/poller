# Backbone Poller

The open source polling agent for [Backbone NMS](https://backbonenms.com). Deploy it anywhere inside your management network and it will monitor every device you add to the Backbone dashboard over ICMP and SNMP. It needs no inbound ports and no database, and it reports to the Backbone polling servers rather than the dashboard, so monitoring keeps working while the dashboard is offline.

## How it works

1. Every polling interval, the poller asks a polling server for the devices assigned to it (`GET /api/poller/devices`), authenticated with the access token you generated on the dashboard's **Pollers** page. The interval itself is an organization setting on the dashboard, so it can be changed without touching the poller.
2. Each device is pinged (three echoes, for reachability and latency) and, unless it is ping-only, polled over SNMP v1, v2c or v3 with the credentials stored on the dashboard. A device can have several credentials to try (the organization's shared communities, for example): they are tried in the order the polling server sends them until one answers, and the poller reports which one worked so it is put first next time. Up to `snmp.concurrency` devices are polled at once. Devices that were down or unknown at their last poll (marked `priority` by the polling server) are started first so outages and recoveries show up sooner, and the rest take any free slots as soon as they open. Only the table columns the dashboard uses are requested, since some agents (Juniper EX switches especially) answer slowly per value, and a device that did not answer SNMP on its last poll is probed without retries.

   Every device gets a **status poll** each cycle: the system details, interface status and traffic counters, and CPU, memory and storage. A **full discovery** also walks the tables that rarely change (IP addresses, LLDP neighbors, inventory and transceiver light levels). The polling server marks the devices due for discovery in the device list, and the poller discovers them in the background, up to `snmp.discovery_concurrency` at a time, with requested rediscoveries first, then devices never discovered, then the ones discovered longest ago. A device being discovered is left out of that cycle's status poll.
3. Results are pushed back to the polling server in batches (`POST /api/poller/results`), several at a time and gzip compressed when the server supports it. Uploads run alongside polling, so a slow upload does not hold up polling.

Several polling server URLs can be configured. The poller keeps using the one that answered last and fails over to the others, in order, when it cannot be reached or responds with a server error. Resubmitting a batch to another server after a timeout is safe, as the servers ignore results they already stored.

Only outbound HTTPS to the polling servers is required. Interface throughput is calculated on the poller from the difference in octet counters between polls, so the first poll after a restart reports counters without rates.

### What is collected

| Data | MIB |
| --- | --- |
| Name, description, contact, location, uptime | SNMPv2-MIB `system` |
| Interfaces with status, speed, counters, errors and throughput | IF-MIB `ifTable` / `ifXTable` |
| IPv4 addresses per interface | IP-MIB `ipAddrTable` |
| Manufacturer, model, serial number and firmware | ENTITY-MIB, plus vendor detection from `sysObjectID`; TIMETRA-CHASSIS-MIB (Nokia SR OS), ASAM-EQUIP-MIB and ASAM-SYSTEM-MIB (Nokia ISAM) |
| CPU load, memory and storage usage | HOST-RESOURCES-MIB; TIMETRA-SYSTEM-MIB (Nokia SR OS) and ASAM-SYSTEM-MIB (Nokia ISAM) for CPU and memory |
| Directly connected neighbors | LLDP-MIB |
| Transceiver receive and transmit light levels, per lane | ENTITY-SENSOR-MIB, CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB, MIKROTIK-MIB, TIMETRA-PORT-MIB (Nokia SR OS) |
| Transceiver low and high alarm thresholds for those light levels | CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB, TIMETRA-PORT-MIB (Nokia SR OS) |

Nokia SR OS routers and switches (7750 SR, 7250 IXR, 7210 SAS and others) and ISAM access nodes and OLTs (7302, 7330, 7360 FX) implement neither HOST-RESOURCES-MIB nor, on SR OS, the standard hardware and sensor MIBs, so their own MIBs are read instead when the standard ones come back empty. Model and software version on SR OS come from the `TiMOS-...` system description.

## Installation

Create a poller on the dashboard's **Pollers** page and copy its access token. The token is only shown once.

### Docker

```sh
docker run -d --name backbone-poller --restart unless-stopped \
  -e POLLING_SERVER_URLS=https://poll.backbonenms.com \
  -e POLLING_SERVER_TOKEN=bbp_... \
  ghcr.io/backbonenms/poller:latest
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

Logs are written to stdout as JSON.

## Development

```sh
go vet ./...
go test ./...
make build
```

The data sent to the polling server is defined in [src/api/defs.go](src/api/defs.go), which must stay in sync with the polling server's copy. SNMP collection from the standard MIBs lives in [src/snmp.go](src/snmp.go), with the shared SNMP primitives in [src/snmp](src/snmp) and transceiver light levels in [src/optics](src/optics).

### Adding a vendor

Everything vendor-specific lives in [src/vendors](src/vendors), one file per vendor, each defining a `Vendor` ([vendor.go](src/vendors/vendor.go)) and listed in `registry` there. A device's vendor is identified by the enterprise number in its `sysObjectID`, and each enterprise number can belong to only one vendor. Only `Name` and `EnterpriseNumbers` are required; the rest are optional hooks, which only fill in what the standard MIBs did not:

| Field | Called | Use |
| --- | --- | --- |
| `Model` | Discoveries, when ENTITY-MIB has no model | Read the model out of the `sysDescr` |
| `Hardware` | Discoveries | Serial number and versions from the vendor's own MIBs |
| `Resources` | Every poll, when HOST-RESOURCES-MIB lacks CPU or memory | CPU load and memory from the vendor's own MIBs |
| `Optics` | Discoveries | Light levels from the vendor's own tables, into `optics.Readings` keyed by ifIndex |
| `SensorTables` | Discoveries, when `Optics` finds nothing | Variants of ENTITY-SENSOR-MIB, described as `optics.SensorTable`s, tried before the standard one |

Read tables with `snmp.WalkTable` (requesting only the columns used) and scalars with `snmp.GetRow`. To test a vendor against a simulated device, serve the values from a real device's `snmpwalk -On` with [snmptest](src/snmp/snmptest/agent.go): `snmptest.NewClient` returns a client to call the hooks with, as in [nokia_sros_test.go](src/vendors/nokia_sros_test.go).

## License

[MIT](LICENSE)
