# What's Authmap?
Authmap is a tiny service that monitors `/var/log/auth.log` for fresh ssh authentication attempts. It checks each IP address against a GeoIP database and logs the resulting latitude, longitude, and country to an influxdb time-series database. This allows viewing in Grafana with the [worldmap plugin](https://grafana.com/grafana/plugins/grafana-worldmap-panel). It could also be used to generate alerts on successful logins.

# Installation
While you could clone this repo and run `go install cmd/authmap/authmap.go`, I would recommend running the service inside Docker.

1. Download GeoLite2-City.mmdb: https://dev.maxmind.com/geoip/geoip2/geolite2/
2. Run the container!
    ```
    docker run --name authmap -v /var/log/auth.log:/var/log/auth.log:ro -v $PATH_TO_GEOLITE:/etc/authmap/GeoLite2-City.mmdb -v $PATH_TO_CONFIG:/etc/authmap/config.yaml
    ```
3. The container will fill out your config file with defaults and may not start succesfully the first time as a result.
4. Edit the settings in config.yaml to set Authmap up to your liking.

# Docker-Compose
```
networks:
  influx:

services:
  influxdb:
    image: influxdb:latest
    ports:
      - '8086:8086'
    volumes:
      - influxdb-storage:/var/lib/influxdb
      - ./influxdb/influxdb.conf:/etc/influxdb/influxdb.conf:ro
    command: -config /etc/influxdb/influxdb.conf
    networks:
      - influx
    restart: unless-stopped
  authmap:
    image: tgiv014/authmap:latest
    volumes:
      - /var/log/auth.log:/var/log/auth.log:ro
      - ./authmap/GeoLite2-City.mmdb:/etc/authmap/GeoLite2-City.mmdb
      - ./authmap/config.yaml:/etc/authmap/config.yaml

    depends_on:
      - influxdb
    networks:
      - influx
    restart: unless-stopped
```