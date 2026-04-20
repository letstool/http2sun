# CLAUDE.md — http2sun

This file provides context for AI-assisted development on the `http2sun` project.

---

## Project overview

`http2sun` is a single-binary HTTP gateway that exposes solar position data as a JSON REST API.
It is written entirely in Go with **zero external dependencies** and embeds all static assets
(web UI, favicon, OpenAPI spec) at compile time using `//go:embed` directives.

The server accepts `POST /api/v1/sun` requests with a JSON body containing `latitude`, `longitude`, and optionally `timezone` and `timestamp`
and returns sunrise, sunset, solar noon, twilight times, and sun angles as structured JSON.
All times are converted to the observer's timezone, specified by the `timezone` JSON field (default: UTC).

---

## Repository layout

```
.
├── api/
│   └── swagger.yaml              # OpenAPI 3.1 source (human-editable)
├── build/
│   └── Dockerfile                # Two-stage Docker build (builder + scratch runtime)
├── cmd/
│   └── http2sun/
│       ├── main.go               # Entire application — single file
│       └── static/
│           ├── favicon.png       # Embedded at build time
│           ├── index.html        # Embedded web UI (dark/light, 15 languages)
│           └── openapi.json      # Embedded OpenAPI spec
├── scripts/
│   ├── 000_init.sh               # go mod tidy
│   ├── 999_test.sh               # Integration smoke tests (curl + jq)
│   ├── linux_build.sh            # Native static binary build
│   ├── linux_run.sh              # Run binary on Linux
│   ├── docker_build.sh           # Build Docker image
│   ├── docker_run.sh             # Run Docker container
│   ├── windows_build.cmd         # Native build on Windows
│   └── windows_run.cmd           # Run binary on Windows
├── go.mod                        # No external dependencies
├── go.sum                        # Empty
├── LICENSE                       # MIT
├── README.md
└── CLAUDE.md                     # This file
```

---

## Key design decisions

- **Single `main.go`**: the entire server logic lives in `cmd/http2sun/main.go`. There are no internal packages. Keep it that way unless the file grows substantially.
- **Embedded assets**: `favicon.png`, `index.html`, and `openapi.json` are embedded with `//go:embed`. Any change to these files is picked up at the next `go build`.
- **Zero external dependencies**: the solar position algorithm is implemented directly in `main.go`. Do not add the `github.com/maltegrosse/go-spa` or any other dependency unless strictly necessary. The `go.sum` file is intentionally empty.
- **Static binary**: the build uses `-tags netgo` and `-ldflags "-extldflags -static"` to produce a fully self-contained binary with no libc dependency. Do not introduce `cgo` dependencies.
- **No framework**: the HTTP layer uses only the standard library (`net/http`). Do not add a router or web framework.
- **POST with JSON body**: the `/api/v1/sun` endpoint uses HTTP POST. The request body is a JSON object with `latitude`, `longitude`, and optionally `timezone` and `timestamp`. Pointer types (`*float64`, `*int64`) are used in `SunRequest` to distinguish omitted fields from zero values.

---

## Solar Position Algorithm

The algorithm is based on the **NOAA Solar Position Algorithm**, an implementation of the method from:

> Jean Meeus, *Astronomical Algorithms*, 2nd Edition (1998), Willmann-Bell.

Accuracy: ±1–2 minutes for dates within ±50 years of J2000.0.

### Key functions in `main.go`

| Function | Description |
|---|---|
| `julianDay(t time.Time) float64` | Convert UTC time to Julian Day Number |
| `solarData(jd float64) (decl, eqtime float64)` | Compute declination and equation of time |
| `hourAngle(lat, decl, zenith float64) (ha, ok, polarDay bool)` | Hour angle for a given zenith angle |
| `minutesToTime(ref time.Time, min float64) time.Time` | Minutes-from-midnight-UTC to absolute time |
| `riseAzimuth(lat, decl float64) float64` | Sunrise azimuth from north (sunset = 360 − rise) |
| `noonElevation(lat, decl float64) float64` | Sun elevation at solar noon |
| `computeSolar(lat, lon float64, loc *time.Location, date time.Time) SunResponse` | Main computation — calls all of the above |

### Zenith angles

| Event | Zenith angle |
|---|---|
| Sunrise / Sunset | 90.833° (includes refraction + solar disc) |
| Civil twilight | 96° |
| Nautical twilight | 102° |
| Astronomical twilight | 108° |

### Polar conditions

`hourAngle` returns `(0, false, false)` for polar night (cos(HA) > 1) and `(180, false, true)` for polar day (cos(HA) < −1). `computeSolar` sets `PolarDay`/`PolarNight` flags and leaves the corresponding time fields as empty strings.

---

## Environment variables & CLI flags

| Environment variable | CLI flag        | Default          | Description |
|----------------------|-----------------|------------------|-------------|
| `LISTEN_ADDR`        | `--listen-addr` | `127.0.0.1:8080` | Listen address. A bare port (e.g. `8080`) is accepted. |

CLI flags are parsed with the standard library `flag` package. Any new configuration entry must expose both a flag and its environment variable counterpart.

---

## Build & run commands

```bash
# Initialise / tidy dependencies
bash scripts/000_init.sh

# Build native static binary → ./out/http2sun
bash scripts/linux_build.sh

# Run (sets LISTEN_ADDR=0.0.0.0:8080)
bash scripts/linux_run.sh

# Build Docker image → letstool/http2sun:latest
bash scripts/docker_build.sh

# Run Docker container
bash scripts/docker_run.sh

# Smoke tests (server must be running)
bash scripts/999_test.sh
```

---

## API contract

### Endpoint

```
POST /api/v1/sun
Content-Type: application/json
```

### Query parameters

| Parameter   | Required | Default  | Notes |
|-------------|----------|----------|-------|
| `latitude`  | ✅       | —        | float, −90 to +90 |
| `longitude` | ✅       | —        | float, −180 to +180 |
| `timezone`  | ❌       | `UTC`    | IANA timezone name of the observer's location. All output times are in this timezone. Loaded via `time.LoadLocation`. | IANA timezone name, loaded via `time.LoadLocation` |
| `timestamp` | ❌       | now      | Integer Unix seconds (UTC). Parsed from JSON as `*int64` via `json.Decode`. |

### Response time fields

All time fields are formatted as `"HH:MM:SS"` in the requested timezone.
An **empty string `""`** means the event does not occur on this date (polar condition).

### Error response

```json
{ "error": "human-readable message" }
```

HTTP status codes: `400` for bad request, `405` for wrong method.

### Other endpoints

| Method | Path            | Description                     |
|--------|-----------------|---------------------------------|
| `GET`  | `/`             | Embedded interactive web UI     |
| `GET`  | `/openapi.json` | OpenAPI 3.1 specification       |
| `GET`  | `/favicon.png`  | Application icon                |

---

## Web UI

The UI is a self-contained single-file HTML/JS/CSS application embedded in the binary.

- **Themes**: dark and light, switchable via toggle.
- **Languages**: 15 locales — Arabic, Bengali, Chinese, German, English, Spanish, French, Hindi, Indonesian, Japanese, Korean, Portuguese, Russian, Urdu, Vietnamese. All translations are in the `TR` object in `index.html`.
- **RTL support**: Arabic and Urdu switch the layout to right-to-left.
- **Sun arc diagram**: SVG showing the sun's path across the sky, with rise/set markers and noon dot.
- **Timeline bar**: 24-hour colour-coded bar showing night, astronomical, nautical, civil twilight, and day.
- **Data cards**: all solar events and angles displayed as individual cards.
- **Raw JSON toggle**: shows the raw API response.

To modify the UI, edit `cmd/http2sun/static/index.html` and rebuild.
To update the API spec, edit `api/swagger.yaml`, copy to `openapi.json`, and rebuild.

---

## Constraints & conventions

- Go version: **1.24+**
- No `cgo`. Keep `CGO_ENABLED=0`.
- No additional HTTP frameworks or routers.
- **No external Go dependencies**. The algorithm is self-contained.
- `SunRequest` uses pointer types (`*float64`, `*int64`) to distinguish missing fields from zero values.
- `json.NewDecoder(r.Body).Decode(&req)` parses the request. No third-party library needed.
- All logic stays in `cmd/http2sun/main.go`.
- Error responses always return `{ "error": "..." }` JSON — never plain-text.
- All time outputs are `"HH:MM:SS"` strings, never Unix timestamps or RFC3339.
- Polar condition fields use empty strings, never `null` or omission.
- `timestamp` in the response is the Unix timestamp of UTC midnight of the target **local** date (not the input timestamp verbatim), so it can be used as a stable day key.
- All code, identifiers, comments, and documentation must be written in **English**.
- **Every configuration environment variable must have a corresponding CLI flag**.

---

## AI-assisted development

This project was developed with the assistance of **Claude Sonnet 4.6** by Anthropic.
