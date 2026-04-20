// SPDX-License-Identifier: MIT

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

	// Standard solar zenith angles (degrees)
	zenithSunrise    = 90.833 // sunrise/sunset (refraction + solar disc radius)
	zenithCivil      = 96.0
	zenithNautical   = 102.0
	zenithAstronomic = 108.0
)

// ---------------------------------------------------------------------------
//  Solar Position Algorithm (NOAA / Jean Meeus, "Astronomical Algorithms")
//  Accurate to ±1–2 minutes for dates within ±50 years of J2000.0.
// ---------------------------------------------------------------------------

// julianDay converts a UTC time.Time to a Julian Day Number.
func julianDay(t time.Time) float64 {
	t = t.UTC()
	y := float64(t.Year())
	m := float64(t.Month())
	d := float64(t.Day()) + float64(t.Hour())/24.0 + float64(t.Minute())/1440.0 + float64(t.Second())/86400.0
	if m <= 2 {
		y--
		m += 12
	}
	A := math.Floor(y / 100)
	B := 2 - A + math.Floor(A/4)
	return math.Floor(365.25*(y+4716)) + math.Floor(30.6001*(m+1)) + d + B - 1524.5
}

// solarData computes the sun's declination (degrees) and equation of time (minutes)
// for the given Julian Day Number.
func solarData(jd float64) (decl, eqtime float64) {
	T := (jd - 2451545.0) / 36525.0

	// Geometric mean longitude (degrees)
	L0 := math.Mod(280.46646+T*(36000.76983+T*0.0003032), 360)
	if L0 < 0 {
		L0 += 360
	}

	// Mean anomaly (degrees)
	M := math.Mod(357.52911+T*(35999.05029-T*0.0001537), 360)
	if M < 0 {
		M += 360
	}
	Mr := M * deg2rad

	// Eccentricity of Earth's orbit
	e := 0.016708634 - T*(0.000042037+T*0.0000001267)

	// Equation of center
	C := math.Sin(Mr)*(1.914602-T*(0.004817+T*0.000014)) +
		math.Sin(2*Mr)*(0.019993-T*0.000101) +
		math.Sin(3*Mr)*0.000289

	// True longitude → apparent longitude
	omega := 125.04 - 1934.136*T
	lambda := (L0 + C) - 0.00569 - 0.00478*math.Sin(omega*deg2rad)

	// Mean obliquity of the ecliptic (degrees) → apparent obliquity (radians)
	eps0 := 23.0 + (26.0+(21.448-T*(46.815+T*(0.00059-T*0.001813)))/60.0)/60.0
	eps := (eps0 + 0.00256*math.Cos(omega*deg2rad)) * deg2rad

	// Declination (degrees)
	decl = math.Asin(math.Sin(eps)*math.Sin(lambda*deg2rad)) * rad2deg

	// Equation of time (minutes)
	y := math.Tan(eps / 2)
	y *= y
	l0r := L0 * deg2rad
	eqtime = 4 * rad2deg * (y*math.Sin(2*l0r) -
		2*e*math.Sin(Mr) +
		4*e*y*math.Sin(Mr)*math.Cos(2*l0r) -
		0.5*y*y*math.Sin(4*l0r) -
		1.25*e*e*math.Sin(2*Mr))
	return
}

// hourAngle computes the hour angle (degrees) at which the sun crosses a given
// zenith angle for an observer at the given latitude.
//
//   - (ha, true,  false) → normal rise/set event.
//   - (0,  false, false) → polar night for this zenith (sun never reaches it).
//   - (180,false, true)  → midnight sun (sun never drops below this zenith).
func hourAngle(lat, decl, zenith float64) (ha float64, ok bool, polarDay bool) {
	cosHA := (math.Cos(zenith*deg2rad) - math.Sin(lat*deg2rad)*math.Sin(decl*deg2rad)) /
		(math.Cos(lat*deg2rad) * math.Cos(decl*deg2rad))
	switch {
	case cosHA > 1:
		return 0, false, false
	case cosHA < -1:
		return 180, false, true
	default:
		return math.Acos(cosHA) * rad2deg, true, false
	}
}

// minutesToTime converts minutes-from-midnight-UTC on the calendar date of
// refUTC into an absolute UTC time.Time.
func minutesToTime(refUTC time.Time, minutes float64) time.Time {
	midnight := time.Date(refUTC.Year(), refUTC.Month(), refUTC.Day(), 0, 0, 0, 0, time.UTC)
	sec := int64(math.Round(minutes * 60))
	return midnight.Add(time.Duration(sec) * time.Second)
}

// riseAzimuth returns the sunrise azimuth from north clockwise (degrees),
// using the identity cos(Az) = sin(decl) / cos(lat) at altitude ≈ 0.
// Sunset azimuth = 360° − riseAzimuth.
func riseAzimuth(lat, decl float64) float64 {
	val := math.Max(-1, math.Min(1, math.Sin(decl*deg2rad)/math.Cos(lat*deg2rad)))
	return math.Acos(val) * rad2deg
}

// noonElevation returns the sun's elevation above the horizon at solar noon (degrees).
func noonElevation(lat, decl float64) float64 {
	return math.Asin(math.Sin(lat*deg2rad)*math.Sin(decl*deg2rad)+
		math.Cos(lat*deg2rad)*math.Cos(decl*deg2rad)) * rad2deg
}

// ---------------------------------------------------------------------------
//  Response types
// ---------------------------------------------------------------------------

// SunResponse is the JSON body returned by GET /api/v1/sun.
// Time fields are formatted as "HH:MM:SS" in the requested timezone.
// An empty string means the event does not occur on this day (polar condition).
type SunResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
	Timestamp int64   `json:"timestamp"`
	Date      string  `json:"date"`

	Sunrise   string `json:"sunrise"`
	Sunset    string `json:"sunset"`
	SolarNoon string `json:"solar_noon"`
	DayLength string `json:"day_length"`

	CivilTwilightBegin string `json:"civil_twilight_begin"`
	CivilTwilightEnd   string `json:"civil_twilight_end"`
	NautTwilightBegin  string `json:"nautical_twilight_begin"`
	NautTwilightEnd    string `json:"nautical_twilight_end"`
	AstroTwilightBegin string `json:"astronomical_twilight_begin"`
	AstroTwilightEnd   string `json:"astronomical_twilight_end"`

	SunriseAzimuth float64 `json:"sunrise_azimuth_deg"`
	SunsetAzimuth  float64 `json:"sunset_azimuth_deg"`
	NoonElevation  float64 `json:"noon_elevation_deg"`
	NoonAzimuth    float64 `json:"noon_azimuth_deg"`

	PolarDay   bool `json:"polar_day"`
	PolarNight bool `json:"polar_night"`

	// Instantaneous sun position at the queried timestamp.
	CurrentAzimuth   float64 `json:"current_azimuth_deg"`
	CurrentElevation float64 `json:"current_elevation_deg"`
	CurrentTimeLocal string  `json:"current_time_local"` // HH:MM:SS in the requested timezone
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

	log.Printf("Sun-over-HTTP API listening on %s", addr)
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

// SunRequest is the JSON body expected by POST /api/v1/sun.
type SunRequest struct {
	Latitude  *float64 `json:"latitude"`  // required, -90..90
	Longitude *float64 `json:"longitude"` // required, -180..180
	Timezone  string   `json:"timezone"`  // optional, IANA name, default UTC
	Timestamp *int64   `json:"timestamp"` // optional, Unix seconds UTC, default now
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
		writeError(w, http.StatusBadRequest, "latitude must be in the range [-90, 90]")
		return
	}

	// longitude — required
	if req.Longitude == nil {
		writeError(w, http.StatusBadRequest, "missing required field: longitude")
		return
	}
	lon := *req.Longitude
	if lon < -180 || lon > 180 {
		writeError(w, http.StatusBadRequest, "longitude must be in the range [-180, 180]")
		return
	}

	// timezone — optional, default UTC
	tzName := req.Timezone
	if tzName == "" {
		tzName = "UTC"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown timezone: %q — use an IANA timezone name such as Europe/Paris or America/New_York", tzName))
		return
	}

	// timestamp — optional, default now
	var inputTime time.Time
	if req.Timestamp == nil {
		inputTime = time.Now()
	} else {
		inputTime = time.Unix(*req.Timestamp, 0)
	}

	// targetDate determines which calendar day to use for rise/set computation.
	targetDate := inputTime.In(loc)
	resp := computeSolar(lat, lon, loc, targetDate)

	// Instantaneous sun position at the exact queried moment.
	az, el := sunPositionAt(lat, lon, inputTime)
	round2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	resp.CurrentAzimuth = round2(az)
	resp.CurrentElevation = round2(el)
	resp.CurrentTimeLocal = inputTime.In(loc).Format("15:04:05")

	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
//  Solar computation
// ---------------------------------------------------------------------------

// computeSolar runs the full solar position algorithm and returns the populated
// SunResponse for the given location, timezone, and calendar date.
func computeSolar(lat, lon float64, loc *time.Location, date time.Time) SunResponse {
	// Reference Julian Day: noon UTC on the target calendar date.
	refUTC := time.Date(date.Year(), date.Month(), date.Day(), 12, 0, 0, 0, time.UTC)
	jd := julianDay(refUTC)
	decl, eqtime := solarData(jd)

	// Solar noon in minutes from midnight UTC.
	noonMin := 720.0 - 4.0*lon - eqtime
	noonUTC := minutesToTime(refUTC, noonMin)

	// Local format helpers.
	fmtTime := func(t time.Time) string { return t.In(loc).Format("15:04:05") }
	fmtDur := func(d time.Duration) string {
		d = d.Round(time.Second)
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	round2 := func(v float64) float64 { return math.Round(v*100) / 100 }

	// Canonical UTC midnight of the target local date — used as the echoed timestamp.
	utcMidnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc).UTC()

	resp := SunResponse{
		Latitude:      round2(lat),
		Longitude:     round2(lon),
		Timezone:      loc.String(),
		Timestamp:     utcMidnight.Unix(),
		Date:          date.In(loc).Format("2006-01-02"),
		SolarNoon:     fmtTime(noonUTC),
		NoonElevation: round2(noonElevation(lat, decl)),
	}

	// Noon azimuth: 180° (south) when lat ≥ decl, 0° (north) otherwise.
	if lat >= decl {
		resp.NoonAzimuth = 180.0
	}

	// Sunrise / Sunset.
	ha, ok, isPolarDay := hourAngle(lat, decl, zenithSunrise)
	switch {
	case isPolarDay:
		resp.PolarDay = true
		resp.DayLength = "24:00:00"
	case !ok:
		resp.PolarNight = true
		resp.DayLength = "00:00:00"
	default:
		riseUTC := minutesToTime(refUTC, noonMin-4*ha)
		setUTC := minutesToTime(refUTC, noonMin+4*ha)
		resp.Sunrise = fmtTime(riseUTC)
		resp.Sunset = fmtTime(setUTC)
		resp.DayLength = fmtDur(setUTC.Sub(riseUTC))
		az := riseAzimuth(lat, decl)
		resp.SunriseAzimuth = round2(az)
		resp.SunsetAzimuth = round2(360 - az)
	}

	// Civil twilight.
	if ha, ok, pd := hourAngle(lat, decl, zenithCivil); ok && !pd {
		resp.CivilTwilightBegin = fmtTime(minutesToTime(refUTC, noonMin-4*ha))
		resp.CivilTwilightEnd = fmtTime(minutesToTime(refUTC, noonMin+4*ha))
	}

	// Nautical twilight.
	if ha, ok, pd := hourAngle(lat, decl, zenithNautical); ok && !pd {
		resp.NautTwilightBegin = fmtTime(minutesToTime(refUTC, noonMin-4*ha))
		resp.NautTwilightEnd = fmtTime(minutesToTime(refUTC, noonMin+4*ha))
	}

	// Astronomical twilight.
	if ha, ok, pd := hourAngle(lat, decl, zenithAstronomic); ok && !pd {
		resp.AstroTwilightBegin = fmtTime(minutesToTime(refUTC, noonMin-4*ha))
		resp.AstroTwilightEnd = fmtTime(minutesToTime(refUTC, noonMin+4*ha))
	}

	return resp
}

// sunPositionAt computes the sun's azimuth (from north, clockwise) and elevation
// (degrees above horizon, with atmospheric refraction) for a specific observer
// and moment in time. Uses the same NOAA/Meeus algorithm as the rest of the file.
func sunPositionAt(lat, lon float64, t time.Time) (azimuth, elevation float64) {
	jd := julianDay(t)
	decl, eqtime := solarData(jd)

	// True solar hour angle (degrees).
	// UTC decimal hours of the moment.
	tUTC := t.UTC()
	utcH := float64(tUTC.Hour()) + float64(tUTC.Minute())/60.0 + float64(tUTC.Second())/3600.0
	// Apparent solar time (hours): shift from UTC by longitude and equation of time.
	solarH := utcH + lon/15.0 + eqtime/60.0
	// Hour angle: 0 at solar noon, positive in afternoon.
	H := (solarH - 12.0) * 15.0
	Hr := H * deg2rad
	latR := lat * deg2rad
	declR := decl * deg2rad

	// Sin of elevation (altitude above horizon).
	sinEl := math.Sin(latR)*math.Sin(declR) + math.Cos(latR)*math.Cos(declR)*math.Cos(Hr)
	sinEl = math.Max(-1, math.Min(1, sinEl))
	el := math.Asin(sinEl) * rad2deg

	// Atmospheric refraction correction (Bennett 1982, accurate to 0.07′).
	if el > -1.0 {
		r := 1.02 / math.Tan((el+10.3/(el+5.11))*deg2rad) / 60.0
		el += r
	}
	elevation = el

	// Azimuth from north, clockwise.
	cosEl := math.Cos(elevation * deg2rad)
	if cosEl < 1e-10 {
		// Sun at or very near zenith: azimuth undefined, return 0.
		return
	}
	cosAz := (math.Sin(declR) - math.Sin(latR)*sinEl) / (math.Cos(latR) * cosEl)
	cosAz = math.Max(-1, math.Min(1, cosAz))
	az := math.Acos(cosAz) * rad2deg
	if math.Sin(Hr) > 0 { // afternoon/west half: mirror azimuth
		az = 360 - az
	}
	azimuth = az
	return
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
