# CLAUDE.md — http2sun

This file provides context for AI-assisted development on the `http2sun` project.

---

## Project overview

`http2sun` is a single-binary HTTP gateway that exposes solar position data as a JSON REST API.

It is written in Go, uses **go-spa** (NREL SPA) for calculations, and embeds all static assets
(web UI, favicon, OpenAPI spec) at compile time via `//go:embed`. The server accepts
`POST /api/v1/sun` requests with a JSON body and returns structured solar data.

---

## Solar algorithm

**NREL Solar Position Algorithm (SPA)** — Reda & Andreas, 2004.
Go implementation: [`github.com/maltegrosse/go-spa`](https://github.com/maltegrosse/go-spa).

| Property | Value |
|---|---|
| Azimuth / zenith accuracy | ±0.0003° |
| Rise/set accuracy | < 1 second |
| Valid date range | Year −2000 to 6000 |

### go-spa usage pattern

```go
// Rise / Set / Transit + declination + equation of time
spa, err := gospa.NewSpa(noonLocal, lat, lon,
    elevation, pressure, temperature,
    deltaT, deltaUT1, slope, azmRotation, atmosRefract)
spa.SetSPAFunction(gospa.SpaZaRts)
spa.Calculate()

// Instantaneous position (azimuth/zenith only)
spa2, _ := gospa.NewSpa(inputTime, lat, lon, ...)
spa2.SetSPAFunction(gospa.SpaZa)
spa2.Calculate()
azimuth   := spa2.GetAzimuth()    // degrees, clockwise from north
elevation := 90 - spa2.GetZenith() // degrees above horizon
```

### Twilight computation

go-spa **cannot** compute twilights via the `atmosRefract` hack (validates `|atmosRefract| ≤ 5`,
which rules out civil/nautical/astronomical depression angles).

Twilights are computed using the **hour angle formula** fed with NREL-accurate `GetDelta()`
(geocentric declination) and `GetEot()` (equation of time) from the noon SPA call:

```
cos(H) = (cos(zenith) − sin(lat)·sin(decl)) / (cos(lat)·cos(decl))
```

- Civil: zenith = 96°
- Nautical: zenith = 102°
- Astronomical: zenith = 108°

This is much more accurate than the previous Meeus-only implementation because declination
and EoT now come from the full NREL perturbation tables.

### ΔT auto-estimation

When `delta_t` is not provided by the caller, ΔT is estimated via Espenak & Meeus (2006):

- 2005–2050: `ΔT = 62.92 + 0.32217·u + 0.005589·u²` (u = year − 2000)
- Accuracy: ±5 s for dates within this range

The caller can override with an explicit `delta_t` field in the JSON body.

---

## Repository layout

```
.
├── api/
│   └── swagger.yaml              # OpenAPI 3.1 source
├── build/
│   └── Dockerfile                # Two-stage Docker build (builder + scratch)
├── cmd/
│   └── http2sun/
│       ├── main.go               # Entire application — single file
│       └── static/
│           ├── favicon.png
│           ├── index.html        # Embedded web UI (dark/light, 15 languages)
│           └── openapi.json      # Embedded OpenAPI spec
├── scripts/
│   ├── 000_init.sh               # go mod tidy
│   ├── 999_test.sh               # Integration smoke tests
│   ├── linux_build.sh / linux_run.sh
│   ├── docker_build.sh / docker_run.sh
│   └── windows_build.cmd / windows_run.cmd
├── go.mod                        # Requires go-spa
├── go.sum
├── LICENSE                       # Apache 2.0
├── README.md
└── CLAUDE.md
```

---

## Key design decisions

- **Single `main.go`**: all server logic lives in one file. Keep it that way.
- **Embedded assets**: `//go:embed` directives include static files at build time.
- **Only one external dependency**: `github.com/maltegrosse/go-spa`. No other dependencies.
- **Static binary**: `-tags netgo -extldflags -static`. No cgo.
- **No HTTP framework**: uses only `net/http`.
- **POST with JSON body**: the `/api/v1/sun` endpoint uses HTTP POST. Required fields use pointer types (`*float64`) to distinguish absent from zero.
- **`_ "time/tzdata"`**: embeds the full IANA timezone database in the binary — required for correct timezone resolution on scratch/Alpine Docker images.

---

## API contract

### Endpoint

```
POST /api/v1/sun
Content-Type: application/json
```

### Request fields

| Field         | Required | Default   | Notes |
|---------------|----------|-----------|-------|
| `latitude`    | ✅       | —         | `*float64`, −90 to +90 |
| `longitude`   | ✅       | —         | `*float64`, −180 to +180 |
| `timezone`    | ❌       | `UTC`     | IANA name of the observer's location. All output times in this timezone. Loaded via `time.LoadLocation`. |
| `timestamp`   | ❌       | now       | `*int64`, Unix seconds UTC. `time.Unix(*ts, 0)`. |
| `elevation`   | ❌       | `0.0`     | `*float64`, metres above sea level. Passed to NREL SPA. |
| `pressure`    | ❌       | `1013.25` | `*float64`, hPa. Passed to NREL SPA. |
| `temperature` | ❌       | `12.0`    | `*float64`, °C. Passed to NREL SPA. |
| `delta_t`     | ❌       | estimated | `*float64`, seconds TT−UT1. Auto-estimated via `estimateDeltaT()` if nil. |

### Response time fields

All time fields: `"HH:MM:SS"` in the observer's timezone. Empty string `""` = event does not occur.

### Response echo fields

`observer_elevation_m`, `observer_pressure_hpa`, `observer_temperature_c`, `delta_t_s` echo
back the physical parameters actually used in the NREL SPA computation (defaults or provided values).

### Error response

```json
{ "error": "human-readable message" }
```

HTTP: `400` bad request, `405` wrong method.

### Other endpoints

| `GET /`             | Embedded web UI |
| `GET /openapi.json` | OpenAPI 3.1 spec |
| `GET /favicon.png`  | Icon |

---

## go-spa: what is and isn't exposed

| go-spa getter | Used for |
|---|---|
| `GetAzimuth()` | Current sun azimuth (clockwise from north) |
| `GetZenith()` | Current sun zenith → elevation = 90 − zenith |
| `GetE()` | Topocentric elevation with refraction → noon elevation |
| `GetDelta()` | Geocentric declination → twilight hour angles |
| `GetEot()` | Equation of time (minutes) → twilight timing |
| `GetSunrise()` | `time.Time` in observer's timezone |
| `GetSunset()` | `time.Time` in observer's timezone |
| `GetSuntransit()` | Decimal local hours of solar noon |
| `GetSrha()` | Sunrise hour angle (−99999 = polar condition) |

**Polar detection**: `cosHourAngle(lat, decl, 90.833)` — if > 1 → polar night, if < −1 → polar day.

---

## Build commands

```bash
bash scripts/000_init.sh        # go mod tidy
bash scripts/linux_build.sh     # → ./out/http2sun
bash scripts/linux_run.sh       # LISTEN_ADDR=0.0.0.0:8080
bash scripts/docker_build.sh    # letstool/http2sun:latest
bash scripts/999_test.sh        # smoke tests (server must be running)
```

---

## Constraints

- Go **1.24+**
- `CGO_ENABLED=0`
- No new HTTP frameworks or routers
- No new Go dependencies beyond `go-spa`
- Error responses always `{ "error": "..." }` JSON
- All time outputs `"HH:MM:SS"`, never Unix or RFC3339
- Polar condition fields: empty strings, never `null`
- All code and documentation in **English**
- Every config env var must have a corresponding CLI flag

---

## AI-assisted development

Developed with **Claude Sonnet 4.6** by Anthropic.
