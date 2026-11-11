# Backbone Poller

The open source polling agent for [Backbone NMS](https://backbonenms.com). Deploy it anywhere inside your management network and it will monitor every device you add to the Backbone dashboard over ICMP and SNMP. It needs no inbound ports and no database.

## How it works

1. Every polling interval, the poller asks the dashboard for the devices assigned to it (`GET /api/poller/devices`), authenticated with the access token you generated on the dashboard's **Pollers** page. The interval itself is an organization setting on the dashboard, so it can be changed without touching the poller.
2. Each device is pinged (three echoes, for reachability and latency) and walked over SNMP v1, v2c or v3 with the credentials stored on the dashboard. A device can have several credentials to try (the organization's shared communities, for example): they are tried in the order the dashboard sends them until one answers, and the poller reports which one worked so the dashboard puts it first next time. Up to `snmp.concurrency` devices are polled at once.
3. Results are pushed back to the dashboard in batches (`POST /api/poller/results`).

Only outbound HTTPS to the dashboard is required. Interface throughput is calculated on the poller from the difference in octet counters between polls, so the first poll after a restart reports counters without rates.

### What is collected

| Data | MIB |
| --- | --- |
| Name, description, contact, location, uptime | SNMPv2-MIB `system` |
| Interfaces with status, speed, counters, errors and throughput | IF-MIB `ifTable` / `ifXTable` |
| IPv4 addresses per interface | IP-MIB `ipAddrTable` |
| Manufacturer, model, serial number and firmware | ENTITY-MIB, plus vendor detection from `sysObjectID` |
| CPU load, memory and storage usage | HOST-RESOURCES-MIB |
| Directly connected neighbors | LLDP-MIB |

## Installation

Create a poller on the dashboard's **Pollers** page and copy its access token. The token is only shown once.

### Docker

```sh
docker run -d --name backbone-poller --restart unless-stopped \
  -e DASHBOARD_URL=https://app.backbonenms.com \
  -e DASHBOARD_TOKEN=bbp_... \
  ghcr.io/backbonenms/poller:latest
```

### From source

Requires Go 1.25 and the `ping` command (iputils on Linux).

```sh
make build
cp config.example.yml config.yml   # then set dashboard.token
./bin/main
```

To run it as a service, create a systemd unit that runs `bin/main` from the directory containing `config.yml`.

## Configuration

Settings are read from `config.yml` in the working directory, if it exists. Environment variables override the file.

| Key | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `dashboard.url` | `DASHBOARD_URL` | | Base URL of the Backbone dashboard |
| `dashboard.token` | `DASHBOARD_TOKEN` | | Poller access token from the dashboard |
| `dashboard.timeout` | | `30s` | Timeout for each request to the dashboard |
| `snmp.concurrency` | | `32` | Maximum number of devices polled at the same time |
| `snmp.timeout` | | `5s` | Timeout for each SNMP request |
| `snmp.retries` | | `1` | Retries for each SNMP request |

Logs are written to stdout as JSON.

## Development

```sh
go vet ./...
make build
```

The data sent to the dashboard is defined in [src/dashboard/defs.go](src/dashboard/defs.go), and SNMP collection lives in [src/snmp.go](src/snmp.go).

## License

[MIT](LICENSE)
