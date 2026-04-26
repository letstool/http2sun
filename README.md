# http2sun

> **Solar Data over HTTP** — A lightweight, stateless HTTP gateway that exposes solar position data (sunrise, sunset, twilights, and sun angles) as a JSON REST API.

Powered by the **NREL Solar Position Algorithm** (SPA) via [go-spa](https://github.com/maltegrosse/go-spa) — accurate to **±0.0003°** in azimuth and zenith angle (Reda & Andreas, 2004). Valid for years −2000 to 6000.

The binary embeds a static web UI and an OpenAPI specification — no runtime files required.

---

## Disclaimer

This project is released **as-is**, for demonstration or reference purposes.
It is **not maintained**: no bug fixes, dependency updates, or new features are planned.

---

## License

MIT License — see [`LICENSE`](LICENSE) for details.

---

## Features

- **NREL Solar Position Algorithm (SPA)** — ±0.0003° accuracy (Reda & Andreas, 2004)
- Optional physical parameters: observer **elevation**, atmospheric **pressure**, air **temperature**
- **ΔT auto-estimation** using Espenak & Meeus (2006) polynomial when not provided
- Solar events: **sunrise, sunset, solar noon, day length**
- Twilight times: **civil, nautical, and astronomical** (begin and end)
- Sun angles: **sunrise/sunset azimuth, noon elevation, noon azimuth**
- **Instantaneous position**: azimuth and elevation at any Unix timestamp
- **Polar conditions**: explicit `polar_day` and `polar_night` flags
- Single static binary — no external runtime files
- Embedded web UI (dark/light mode, 15 languages with auto-detection) and OpenAPI 3.1 spec
- Docker image built on `scratch` — minimal attack surface

---

## Algorithm

Solar calculations use the **NREL Solar Position Algorithm** (SPA), the industry standard:

> I. Reda and A. Andreas, *Solar Position Algorithm for Solar Radiation Applications*, Solar Energy, Vol. 76(5), 2004; pp. 577–589.

Go implementation: [go-spa](https://github.com/maltegrosse/go-spa) by Malte Grosse.

| Property | NREL SPA (this project) |
|---|---|
| Azimuth & zenith accuracy | **±0.0003°** |
| Rise/set accuracy | **< 1 second** |
| Valid date range | **Year −2000 to 6000** |
| Observer elevation | ✅ |
| Atmospheric pressure | ✅ |
| Air temperature | ✅ |
| ΔT (TT − UT1) | ✅ auto-estimated or user-provided |
| Atmospheric refraction | ✅ NREL formula |

### ΔT estimation

When `delta_t` is not provided, ΔT is estimated using the Espenak & Meeus (2006) polynomial:

- 2005–2050: `ΔT = 62.92 + 0.32217·u + 0.005589·u²` (u = year − 2000)
- Accuracy: ±5 s for dates within 2005–2050 (current actual value ≈ 69 s in 2026)

---

## Build

### Prerequisites

- [Go](https://go.dev/dl/) **1.24+**
- Internet access to `github.com` (for go-spa dependency, only needed at build time)

### Native binary (Linux)

```bash
bash scripts/linux_build.sh
```

```bash
GOPROXY=direct GONOSUMDB='*' go build \
    -trimpath \
    -ldflags="-extldflags -static -s -w" \
    -tags netgo \
    -o ./out/http2sun ./cmd/http2sun
```

### Docker image

```bash
bash scripts/docker_build.sh
```

---

## Run

```bash
bash scripts/linux_run.sh
# or
docker run -p 8080:8080 letstool/http2sun:latest
```

Open [http://localhost:8080](http://localhost:8080) for the web UI, or query the API directly.

---

## Configuration

| CLI flag        | Environment variable | Default          | Description                              |
|-----------------|----------------------|------------------|------------------------------------------|
| `--listen-addr` | `LISTEN_ADDR`        | `127.0.0.1:8080` | Address and port the HTTP server listens on |

---

## API Reference

### `POST /api/v1/sun`

Returns solar position data for the given location and time.

#### Request body (JSON)

| Field         | Required | Default   | Description                                                                                |
|---------------|----------|-----------|--------------------------------------------------------------------------------------------|
| `latitude`    | ✅       | —         | Decimal degrees, −90 to +90                                                               |
| `longitude`   | ✅       | —         | Decimal degrees, −180 to +180                                                              |
| `timezone`    | ❌       | `UTC`     | IANA timezone name of the observer's location. All output times are in this timezone.     |
| `timestamp`   | ❌       | now       | Unix timestamp in seconds (UTC epoch). Determines target day and instantaneous position.  |
| `elevation`   | ❌       | `0`       | Observer altitude above sea level in **metres**. Affects NREL refraction correction.     |
| `pressure`    | ❌       | `1013.25` | Atmospheric pressure in **hPa**. Affects refraction correction.                           |
| `temperature` | ❌       | `12.0`    | Air temperature in **°C**. Affects refraction correction.                                 |
| `delta_t`     | ❌       | estimated | ΔT = TT − UT1 in **seconds**. Auto-estimated via Espenak & Meeus (2006) if omitted.     |

#### Example requests

```bash
# Minimal — Paris, current time
curl -X POST http://localhost:8080/api/v1/sun \
  -H "Content-Type: application/json" \
  -d '{"latitude": 48.8566, "longitude": 2.3522, "timezone": "Europe/Paris"}'

# Full — high-altitude station with all physical parameters
curl -X POST http://localhost:8080/api/v1/sun \
  -H "Content-Type: application/json" \
  -d '{
    "latitude": 45.8326, "longitude": 6.8652,
    "timezone": "Europe/Paris", "timestamp": 1745107200,
    "elevation": 4808, "pressure": 545.0, "temperature": -15.0, "delta_t": 69.2
  }'
```

#### Example response

```json
{
  "latitude": 48.86,
  "longitude": 2.35,
  "timezone": "Europe/Paris",
  "timestamp": 1745107200,
  "date": "2026-04-20",
  "observer_elevation_m": 0,
  "observer_pressure_hpa": 1013.25,
  "observer_temperature_c": 12,
  "delta_t_s": 75.27,
  "sunrise": "06:49:42",
  "sunset": "20:49:17",
  "solar_noon": "13:49:30",
  "day_length": "13:59:35",
  "civil_twilight_begin": "06:15:47",
  "civil_twilight_end": "21:23:12",
  "nautical_twilight_begin": "05:33:49",
  "nautical_twilight_end": "22:05:10",
  "astronomical_twilight_begin": "04:46:58",
  "astronomical_twilight_end": "22:52:02",
  "sunrise_azimuth_deg": 72.17,
  "sunset_azimuth_deg": 287.83,
  "noon_elevation_deg": 52.76,
  "noon_azimuth_deg": 180,
  "polar_day": false,
  "polar_night": false,
  "current_azimuth_deg": 182.67,
  "current_elevation_deg": 52.74,
  "current_time_local": "13:56:57"
}
```

#### Response fields

| Field                         | Type      | Description                                                                                    |
|-------------------------------|-----------|------------------------------------------------------------------------------------------------|
| `latitude`                    | `number`  | Observer latitude (degrees)                                                                    |
| `longitude`                   | `number`  | Observer longitude (degrees)                                                                   |
| `timezone`                    | `string`  | IANA timezone of the observer's location                                                       |
| `timestamp`                   | `integer` | Unix timestamp of UTC midnight of the target local date                                        |
| `date`                        | `string`  | Target date (YYYY-MM-DD) in the observer's timezone                                            |
| `observer_elevation_m`        | `number`  | Observer altitude used in NREL SPA (metres)                                                    |
| `observer_pressure_hpa`       | `number`  | Atmospheric pressure used in NREL SPA (hPa)                                                   |
| `observer_temperature_c`      | `number`  | Air temperature used in NREL SPA (°C)                                                         |
| `delta_t_s`                   | `number`  | ΔT value used in NREL SPA (seconds). Provided or auto-estimated.                              |
| `sunrise`                     | `string`  | Sunrise time (HH:MM:SS). Empty string during polar night                                       |
| `sunset`                      | `string`  | Sunset time (HH:MM:SS). Empty string during polar day                                          |
| `solar_noon`                  | `string`  | Solar noon (HH:MM:SS)                                                                         |
| `day_length`                  | `string`  | Day length (HH:MM:SS). `00:00:00` = polar night, `24:00:00` = polar day                      |
| `civil_twilight_begin/end`    | `string`  | Civil twilight (sun 6° below horizon). Empty when not applicable.                             |
| `nautical_twilight_begin/end` | `string`  | Nautical twilight (sun 12° below horizon). Empty when not applicable.                         |
| `astronomical_twilight_begin/end` | `string` | Astronomical twilight (sun 18° below horizon). Empty when not applicable.                  |
| `sunrise_azimuth_deg`         | `number`  | Sunrise azimuth, clockwise from north (°). 0 when no sunrise.                                 |
| `sunset_azimuth_deg`          | `number`  | Sunset azimuth, clockwise from north (°). 0 when no sunset.                                   |
| `noon_elevation_deg`          | `number`  | Sun elevation at solar noon (°). Negative during polar night.                                  |
| `noon_azimuth_deg`            | `number`  | Sun azimuth at solar noon — `180` (south) or `0` (north)                                      |
| `polar_day`                   | `boolean` | `true` when the sun does not set (midnight sun)                                                |
| `polar_night`                 | `boolean` | `true` when the sun does not rise                                                              |
| `current_azimuth_deg`         | `number`  | Sun azimuth at the queried timestamp (°), clockwise from north                                 |
| `current_elevation_deg`       | `number`  | Sun elevation at the queried timestamp (°). Negative when below horizon.                       |
| `current_time_local`          | `string`  | Local time at the queried timestamp (HH:MM:SS) in the observer's timezone                     |

### Other endpoints

| Method | Path            | Description                     |
|--------|-----------------|---------------------------------|
| `GET`  | `/`             | Embedded interactive web UI     |
| `GET`  | `/openapi.json` | OpenAPI 3.1 specification       |
| `GET`  | `/favicon.png`  | Application icon                |

---

## Development

```bash
bash scripts/000_init.sh   # go mod tidy
bash scripts/linux_build.sh
bash scripts/linux_run.sh
bash scripts/999_test.sh   # smoke tests (server must be running)
```

---

## AI-Assisted Development

This project was developed with the assistance of **[Claude Sonnet 4.6](https://www.anthropic.com/claude)** by Anthropic.

---

## See also

| Projet | GitHub | Docker Hub | Description |
|---|---|---|---|
| `http2tor` | [letstool/http2tor](https://github.com/letstool/http2tor) | [letstool/http2tor](https://hub.docker.com/r/letstool/http2tor) | Lightweight HTTP gateway exposing Tor network detection as a JSON REST API |
| `http2geoip` | [letstool/http2geoip](https://github.com/letstool/http2geoip) | [letstool/http2geoip](https://hub.docker.com/r/letstool/http2geoip) | Lightweight stateless HTTP gateway exposing IP geolocation as a JSON REST API |
| `http2cert` | [letstool/http2cert](https://github.com/letstool/http2cert) | [letstool/http2cert](https://hub.docker.com/r/letstool/http2cert) | Lightweight stateless HTTP gateway exposing X.509 certificate inspection as a JSON REST API |
| `http2dns` | [letstool/http2dns](https://github.com/letstool/http2dns) | [letstool/http2dns](https://hub.docker.com/r/letstool/http2dns) | Lightweight stateless HTTP gateway exposing DNS queries as a JSON REST API |
| `http2whois` | [letstool/http2whois](https://github.com/letstool/http2whois) | [letstool/http2whois](https://hub.docker.com/r/letstool/http2whois) | Lightweight stateless HTTP gateway exposing WHOIS queries as a JSON REST API |
| `http2sun` | [letstool/http2sun](https://github.com/letstool/http2sun) | [letstool/http2sun](https://hub.docker.com/r/letstool/http2sun) | Lightweight stateless HTTP gateway exposing solar position data as a JSON REST API |
