// main.go
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // embed the full IANA timezone database in the binary

	gospa "github.com/maltegrosse/go-spa"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/favicon.png
var faviconPNG []byte

//go:embed static/openapi.json
var openapiJSON []byte

// ---------------------------------------------------------------------------
//  Constants
// ---------------------------------------------------------------------------

const (
	deg2rad = math.Pi / 180.0
	rad2deg = 180.0 / math.Pi

	// Standard zenith angles (degrees from zenith = depression below horizon + 90)
	zenithSunrise    = 90.833 // geometric sunrise/sunset (refraction + solar disc)
	zenithCivil      = 96.0  // sun 6° below horizon
	zenithNautical   = 102.0 // sun 12° below horizon
	zenithAstronomic = 108.0 // sun 18° below horizon

	// Physical defaults (used when the caller omits the optional fields)
	defaultElevation    = 0.0     // metres above sea level
	defaultPressure     = 1013.25 // hPa (standard atmosphere at sea level)
	defaultTemperature  = 12.0    // °C (ICAO standard atmosphere, low altitude)
	defaultAtmosRefract = 0.5667  // degrees (standard refraction at horizon)
	defaultDeltaUT1     = 0.0     // seconds (UT1 - UTC, usually |x| < 0.9 s)
)

// ---------------------------------------------------------------------------
//  ΔT estimation — Espenak & Meeus (2006) polynomial fit
//  Returns Terrestrial Time minus UT1 in seconds.
// ---------------------------------------------------------------------------

func estimateDeltaT(t time.Time) float64 {
	y := float64(t.Year()) + (float64(t.YearDay())-0.5)/365.25
	switch {
	case y >= 2005 && y < 2050:
		u := y - 2000
		return 62.92 + 0.32217*u + 0.005589*u*u
	case y >= 1986 && y < 2005:
		u := y - 2000
		return 63.86 + 0.3345*u - 0.060374*u*u + 0.0017275*math.Pow(u, 3) +
			0.000651814*math.Pow(u, 4) + 0.00002373599*math.Pow(u, 5)
	case y >= 1961 && y < 1986:
		u := y - 1975
		return 45.45 + 1.067*u - u*u/260 - math.Pow(u, 3)/718
	case y >= 2050:
		u := y - 1820
		return -20 + 32*(u/100)*(u/100)
	default:
		u := y - 1820
		return -20 + 32*(u/100)*(u/100)
	}
}

// ---------------------------------------------------------------------------
//  Hour angle helpers (used for twilight computation)
//  go-spa blocks |atmosRefract| > 5°, so twilights (96°, 102°, 108°) cannot
//  be computed via the library's built-in rise/set. We use the standard hour
//  angle formula, fed with NREL-accurate declination and equation of time.
// ---------------------------------------------------------------------------

// cosHourAngle returns cos(H) where H is the hour angle at which the sun's
// centre crosses the given zenith angle, for an observer at the given latitude
// with the given geocentric declination.
//   > 1  → polar night for this zenith  (sun never rises that high)
//   < -1 → polar day  for this zenith  (sun never sets that low)
func cosHourAngle(lat, decl, zenithDeg float64) float64 {
	return (math.Cos(zenithDeg*deg2rad) -
		math.Sin(lat*deg2rad)*math.Sin(decl*deg2rad)) /
		(math.Cos(lat*deg2rad) * math.Cos(decl*deg2rad))
}

// twilightPair computes the begin and end of a twilight defined by zenithDeg.
// noonMin is solar noon in minutes from UTC midnight on the date of refUTC.
// Returns empty strings when the twilight does not occur (polar conditions).
func twilightPair(refUTC time.Time, lat, lon, decl, eotMin, zenithDeg float64, loc *time.Location) (begin, end string) {
	cHA := cosHourAngle(lat, decl, zenithDeg)
	if cHA >= 1 || cHA <= -1 {
		return "", "" // polar condition for this twilight band
	}
	haDeg := math.Acos(cHA) * rad2deg
	noonMin := 720.0 - 4.0*lon - eotMin
	bTime := minutesToUTC(refUTC, noonMin-4*haDeg)
	eTime := minutesToUTC(refUTC, noonMin+4*haDeg)
	return bTime.In(loc).Format("15:04:05"), eTime.In(loc).Format("15:04:05")
}

// minutesToUTC converts minutes-from-UTC-midnight on the calendar date of ref
// into an absolute UTC time.Time.
func minutesToUTC(ref time.Time, minutes float64) time.Time {
	midnight := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
	return midnight.Add(time.Duration(math.Round(minutes*60)) * time.Second)
}

// riseAzimuth returns the sunrise azimuth (degrees, clockwise from north).
// Sunset azimuth = 360 − riseAzimuth.
func riseAzimuth(lat, decl float64) float64 {
	v := math.Max(-1, math.Min(1, math.Sin(decl*deg2rad)/math.Cos(lat*deg2rad)))
	return math.Acos(v) * rad2deg
}

// ---------------------------------------------------------------------------
//  Request / Response types
// ---------------------------------------------------------------------------

// SunRequest is the JSON body for POST /api/v1/sun.
type SunRequest struct {
	// Required
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`

	// Optional — output timezone
	Timezone string `json:"timezone"` // IANA, default "UTC"

	// Optional — time
	Timestamp *int64 `json:"timestamp"` // Unix seconds UTC, default now

	// Optional — physical observer parameters (affect NREL SPA accuracy)
	Elevation   *float64 `json:"elevation"`    // metres above sea level, default 0
	Pressure    *float64 `json:"pressure"`     // atmospheric pressure hPa, default 1013.25
	Temperature *float64 `json:"temperature"`  // air temperature °C, default 12.0
	DeltaT      *float64 `json:"delta_t"`      // TT − UT1 seconds, auto-estimated if absent
}

// SunResponse is the JSON body returned by POST /api/v1/sun.
type SunResponse struct {
	// Input echo
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Timestamp int64   `json:"timestamp"`
	Date      string  `json:"date"`

	// Physical parameters used in the NREL SPA computation
	ObserverElevationM   float64 `json:"observer_elevation_m"`
	ObserverPressureHpa  float64 `json:"observer_pressure_hpa"`
	ObserverTemperatureC float64 `json:"observer_temperature_c"`
	DeltaTSeconds        float64 `json:"delta_t_s"`

	// Solar events (empty string = event does not occur on this date)
	Sunrise   string `json:"sunrise"`
	Sunset    string `json:"sunset"`
	SolarNoon string `json:"solar_noon"`
	DayLength string `json:"day_length"`

	// Twilight times
	CivilTwilightBegin  string `json:"civil_twilight_begin"`
	CivilTwilightEnd    string `json:"civil_twilight_end"`
	NautTwilightBegin   string `json:"nautical_twilight_begin"`
	NautTwilightEnd     string `json:"nautical_twilight_end"`
	AstroTwilightBegin  string `json:"astronomical_twilight_begin"`
	AstroTwilightEnd    string `json:"astronomical_twilight_end"`

	// Sun angles at key events
	SunriseAzimuth float64 `json:"sunrise_azimuth_deg"`
	SunsetAzimuth  float64 `json:"sunset_azimuth_deg"`
	NoonElevation  float64 `json:"noon_elevation_deg"`
	NoonAzimuth    float64 `json:"noon_azimuth_deg"`

	// Polar conditions
	PolarDay   bool `json:"polar_day"`
	PolarNight bool `json:"polar_night"`

	// Instantaneous position at the queried timestamp
	CurrentAzimuth   float64 `json:"current_azimuth_deg"`
	CurrentElevation float64 `json:"current_elevation_deg"`
	CurrentTimeLocal string  `json:"current_time_local"`
}

// errorResponse is the JSON body returned on 4xx errors.
type errorResponse struct {
	Error string `json:"error"`
}

// ---------------------------------------------------------------------------
//  Configuration
// ---------------------------------------------------------------------------

const envListenAddr = "LISTEN_ADDR"

func main() {
	flagListenAddr := flag.String("listen-addr", "", "Address and port to listen on (overrides "+envListenAddr+")")
	flag.Parse()

	addr := resolveConfig(*flagListenAddr, envListenAddr, "127.0.0.1:8080")
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/favicon.png", faviconHandler)
	http.HandleFunc("/openapi.json", openapiHandler)
	http.HandleFunc("/api/v1/sun", sunHandler)

	log.Printf("Sun-over-HTTP API listening on %s (powered by NREL SPA via go-spa)", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server exited: %v", err)
	}
}

func resolveConfig(flagVal, envKey, fallback string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
//  HTTP handlers
// ---------------------------------------------------------------------------

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Write(faviconPNG)
}
func openapiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(openapiJSON)
}

func sunHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed; use POST")
		return
	}

	var req SunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	// latitude — required
	if req.Latitude == nil {
		writeError(w, http.StatusBadRequest, "missing required field: latitude")
		return
	}
	lat := *req.Latitude
	if lat < -90 || lat > 90 {
		writeError(w, http.StatusBadRequest, "latitude must be in [-90, 90]")
		return
	}

	// longitude — required
	if req.Longitude == nil {
		writeError(w, http.StatusBadRequest, "missing required field: longitude")
		return
	}
	lon := *req.Longitude
	if lon < -180 || lon > 180 {
		writeError(w, http.StatusBadRequest, "longitude must be in [-180, 180]")
		return
	}

	// timezone — optional, default UTC
	tzName := req.Timezone
	if tzName == "" {
		tzName = "UTC"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("unknown timezone: %q — use an IANA name such as Europe/Paris", tzName))
		return
	}

	// timestamp — optional, default now
	var inputTime time.Time
	if req.Timestamp == nil {
		inputTime = time.Now()
	} else {
		inputTime = time.Unix(*req.Timestamp, 0)
	}

	// Physical parameters — optional, use defaults when absent
	elevation := defaultElevation
	if req.Elevation != nil {
		elevation = *req.Elevation
	}
	pressure := defaultPressure
	if req.Pressure != nil {
		pressure = *req.Pressure
	}
	temperature := defaultTemperature
	if req.Temperature != nil {
		temperature = *req.Temperature
	}
	deltaT := estimateDeltaT(inputTime)
	if req.DeltaT != nil {
		deltaT = *req.DeltaT
	}

	resp := computeSolar(lat, lon, loc, inputTime, elevation, pressure, temperature, deltaT)
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
//  Solar computation — powered by NREL SPA via go-spa
// ---------------------------------------------------------------------------

func computeSolar(lat, lon float64, loc *time.Location, inputTime time.Time,
	elevation, pressure, temperature, deltaT float64) SunResponse {

	round2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	fmtDur := func(d time.Duration) string {
		d = d.Round(time.Second)
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}

	// Target local calendar date
	localDay := inputTime.In(loc)

	// UTC midnight of the target local date — used as the reference for twilight maths
	utcMidnight := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 0, 0, 0, 0, loc).UTC()

	// Noon in the observer's timezone — used for rise/set/transit SPA call
	noonLocal := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 12, 0, 0, 0, loc)

	// ── 1. NREL SPA: rise / transit / set + declination + equation of time ──
	//
	// We pass noon local time; go-spa extracts year/month/day/hour + UTC offset.
	// SpaZaRts computes zenith, azimuth, EOT, and rise/transit/set.
	spaRts, err := gospa.NewSpa(
		noonLocal, lat, lon,
		elevation, pressure, temperature,
		deltaT, defaultDeltaUT1,
		0, 0, // slope, azm_rotation (not used here)
		defaultAtmosRefract,
	)
	if err != nil {
		log.Printf("go-spa RTS init error: %v", err)
	} else {
		spaRts.SetSPAFunction(gospa.SpaZaRts)
		if err = spaRts.Calculate(); err != nil {
			log.Printf("go-spa RTS calculate error: %v", err)
		}
	}

	// Accurate geocentric declination and equation of time at noon (minutes).
	// These feed the twilight hour angle computation.
	var decl, eotMin float64
	if err == nil {
		decl = spaRts.GetDelta() // geocentric declination (degrees)
		eotMin = spaRts.GetEot() // equation of time (minutes)
	}

	// ── 2. Polar detection via hour angle formula ──
	cHA := cosHourAngle(lat, decl, zenithSunrise)
	isPolarNight := cHA > 1
	isPolarDay := cHA < -1

	// ── 3. Populate response base ──
	resp := SunResponse{
		Latitude:             round2(lat),
		Longitude:            round2(lon),
		Timezone:             loc.String(),
		Timestamp:            utcMidnight.Unix(),
		Date:                 localDay.Format("2006-01-02"),
		ObserverElevationM:   elevation,
		ObserverPressureHpa:  pressure,
		ObserverTemperatureC: temperature,
		DeltaTSeconds:        round2(deltaT),
		PolarDay:             isPolarDay,
		PolarNight:           isPolarNight,
	}

	// ── 4. Noon elevation & azimuth (from NREL SPA) ──
	noonZenith := 90 - spaRts.GetE() // E = topocentric elevation with refraction
	resp.NoonElevation = round2(90 - noonZenith)
	// Noon azimuth: south (180°) when lat ≥ decl, north (0°) otherwise.
	if lat >= decl {
		resp.NoonAzimuth = 180.0
	}

	// ── 5. Sunrise / Sunset / Solar Noon / Day Length ──
	if !isPolarNight && !isPolarDay && err == nil {
		// go-spa returns time.Time in the observer's timezone (Location from noonLocal)
		srTime := spaRts.GetSunrise()
		ssTime := spaRts.GetSunset()

		resp.Sunrise = srTime.Format("15:04:05")
		resp.Sunset = ssTime.Format("15:04:05")

		// Solar noon from GetSuntransit() (decimal local hours)
		transitHr := spaRts.GetSuntransit()
		totalSec := int(math.Round(transitHr * 3600))
		th := totalSec / 3600
		tm := (totalSec % 3600) / 60
		ts := totalSec % 60
		noonTime := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), th, tm, ts, 0, loc)
		resp.SolarNoon = noonTime.Format("15:04:05")

		// Day length — handle inverted case (day spans midnight)
		dur := ssTime.Sub(srTime)
		if dur < 0 {
			dur += 24 * time.Hour
		}
		resp.DayLength = fmtDur(dur)

		// Azimuth at rise/set
		az := riseAzimuth(lat, decl)
		resp.SunriseAzimuth = round2(az)
		resp.SunsetAzimuth = round2(360 - az)
	} else if isPolarDay {
		resp.DayLength = "24:00:00"
	} else {
		resp.DayLength = "00:00:00"
	}

	// ── 6. Twilights (hour angle formula + NREL-accurate decl & eotMin) ──
	refUTC := noonLocal.UTC() // reference for minutesToUTC
	resp.CivilTwilightBegin, resp.CivilTwilightEnd =
		twilightPair(refUTC, lat, lon, decl, eotMin, zenithCivil, loc)
	resp.NautTwilightBegin, resp.NautTwilightEnd =
		twilightPair(refUTC, lat, lon, decl, eotMin, zenithNautical, loc)
	resp.AstroTwilightBegin, resp.AstroTwilightEnd =
		twilightPair(refUTC, lat, lon, decl, eotMin, zenithAstronomic, loc)

	// ── 7. Instantaneous position at the exact queried timestamp ──
	inputLocal := inputTime.In(loc)
	spaCur, errCur := gospa.NewSpa(
		inputLocal, lat, lon,
		elevation, pressure, temperature,
		deltaT, defaultDeltaUT1,
		0, 0,
		defaultAtmosRefract,
	)
	if errCur == nil {
		spaCur.SetSPAFunction(gospa.SpaZa)
		if errCur = spaCur.Calculate(); errCur == nil {
			resp.CurrentAzimuth = round2(spaCur.GetAzimuth())
			resp.CurrentElevation = round2(90 - spaCur.GetZenith())
		}
	}
	if errCur != nil {
		log.Printf("go-spa Za calculate error: %v", errCur)
	}
	resp.CurrentTimeLocal = inputLocal.Format("15:04:05")

	return resp
}

// ---------------------------------------------------------------------------
//  JSON helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("JSON encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errorResponse{Error: msg})
}
