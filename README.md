# Backbone Poller

The open source polling agent for [Backbone NMS](https://backbonenms.com). Deploy it anywhere inside your management network and it will monitor every device you add to the Backbone dashboard over ICMP and SNMP. It needs no inbound ports and no database, and it reports to the Backbone polling servers rather than the dashboard, so monitoring keeps working while the dashboard is offline.

## How it works

1. Every polling interval, the poller asks a polling server for the devices assigned to it (`GET /api/poller/devices`), authenticated with the access token you generated on the dashboard's **Pollers** page. The interval itself is an organization setting on the dashboard, so it can be changed without touching the poller.
2. Each device is pinged (three echoes, for reachability and latency) and walked over SNMP v1, v2c or v3 with the credentials stored on the dashboard. A device can have several credentials to try (the organization's shared communities, for example): they are tried in the order the polling server sends them until one answers, and the poller reports which one worked so it is put first next time. Up to `snmp.concurrency` devices are polled at once.
3. Results are pushed back to the polling server in batches (`POST /api/poller/results`).

Several polling server URLs can be configured. The poller keeps using the one that answered last and fails over to the others, in order, when it cannot be reached or responds with a server error. Resubmitting a batch to another server after a timeout is safe, as the servers ignore results they already stored.

Only outbound HTTPS to the polling servers is required. Interface throughput is calculated on the poller from the difference in octet counters between polls, so the first poll after a restart reports counters without rates.

### What is collected

| Data | MIB |
| --- | --- |
| Name, description, contact, location, uptime | SNMPv2-MIB `system` |
| Interfaces with status, speed, counters, errors and throughput | IF-MIB `ifTable` / `ifXTable` |
| IPv4 addresses per interface | IP-MIB `ipAddrTable` |
| Manufacturer, model, serial number and firmware | ENTITY-MIB, plus vendor detection from `sysObjectID` |
| CPU load, memory and storage usage | HOST-RESOURCES-MIB |
| Directly connected neighbors | LLDP-MIB |
| Transceiver receive and transmit light levels, per lane | ENTITY-SENSOR-MIB, CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB, MIKROTIK-MIB |
| Transceiver low and high alarm thresholds for those light levels | CISCO-ENTITY-SENSOR-MIB, JUNIPER-DOM-MIB |

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
| `snmp.concurrency` | | `32` | Maximum number of devices polled at the same time |
| `snmp.timeout` | | `5s` | Timeout for each SNMP request |
| `snmp.retries` | | `1` | Retries for each SNMP request |

Logs are written to stdout as JSON.

## Development

```sh
go vet ./...
make build
```

The data sent to the polling server is defined in [src/api/defs.go](src/api/defs.go), which must stay in sync with the polling server's copy,, and SNMP collection lives in [src/snmp.go](src/snmp.go).

## License

[MIT](LICENSE)
