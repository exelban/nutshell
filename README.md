# NutShell

[![Stats](https://serhiy.s3.eu-central-1.amazonaws.com/Github_repo/nutshell/nutshell-list.png)]()
[![Stats](https://serhiy.s3.eu-central-1.amazonaws.com/Github_repo/nutshell/nutshell-details.png)]()

Nutshell is a simple GUI for NUT (Network UPS Tools) that allows you to monitor your UPS devices.

## Features
- Monitor multiple UPS devices (on the same host or different hosts)
- Display UPS status, battery level, and load
- Dark mode support

## Usage

### Docker
Build and run the Docker container:
```sh
docker -e UPSD_HOST=localhost exelban/nutshell
```

### Docker Compose
```yaml
Nutshell:
  image: exelban/nutshell:latest
  restart: unless-stopped
  environment:
    - UPSD_HOST=localhost
```

## Configuration
Nutshell can be configured using environment variables. Here are the available options:
- `UPSD_HOST`: Hostname or IP address of the NUT server (multiple can be specified, separated by commas)
- `UPSD_PORT`: Port of the NUT server (multiple can be specified, separated by commas)
- `UPSD_USERNAME`: Username for the NUT server (multiple can be specified, separated by commas)
- `UPSD_PASSWORD`: Password for the NUT server (multiple can be specified, separated by commas)
- `POOL_INTERVAL` - Interval for polling UPS status (default: `10s`)
- `ADDR` - Address to listen on (default: `localhost`)
- `PORT` - Port to listen on (default: `8833`)
- `DEBUG` - Enable debug mode (default: `false`)

## License
[MIT License](https://github.com/exelban/nutshell/blob/master/LICENSE)
