package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var listen = flag.String("listen", ":9456", "(Host and) port to listen on for Prometheus export")
var connect = flag.String("connect", "192.168.3.41:9456", "Host/port to connect to for sensor readings")
var connectTimeout = flag.Duration("connect-timeout", 30*time.Second, "Connection deadline")

var (
	ds18x20SampleRE   = regexp.MustCompile(`^(?i)-?\d+ (temp) ([0-9a-f]+) (\w+) ([\d.]+)$`)
	dht22SampleRE     = regexp.MustCompile(`^(?i)-?\d+ (humidity) (DHT22) ([\d.]+) ([\d.]+)$`)
	temperatureGauges = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "sensors",
		Name:      "temperature_degrees_celsius",
		Help:      "Temperature sampled from a single sensor, in degrees celsius",
	}, []string{"id", "device", "model"})
	humidityGauges = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "sensors",
		Name:      "relative_humidity_percent",
		Help:      "Relative humidity sampled from a single sensor, in percent",
	}, []string{"id", "device", "model"})
	connectionAttempts = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "sensors",
		Name:      "connection_attempts",
		Help:      "Attempts to connect ot WRT54GL",
	})
	connectionErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "sensors",
		Name:      "connection_errors",
		Help:      "Failures to connect ot WRT54GL",
	})
	samplesReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "sensors",
		Name:      "samples_received",
		Help:      "Samples received by collector",
	})
	bytesReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "sensors",
		Name:      "bytes_received",
		Help:      "Bytes received by collector (not necessarily in samples)",
	})
)

var arduinoDeviceIDRE = regexp.MustCompile(`^(?i)([0-9a-f]{2})([0-9a-f]+)$`)

func formatDevice(ID, model string) string {
	if model == "DHT22" {
		// These don't have IDs and I only had one
		return "dht22"
	}
	if m := arduinoDeviceIDRE.FindStringSubmatch(ID); m != nil {
		if deviceID, err := strconv.ParseUint(m[2], 16, 64); err != nil {
			log.Printf("unparseable device ID %q: %v", m[2], err)
			return ID
		} else {
			return strings.ToLower(model) + "-" + fmt.Sprintf("%012x", deviceID)
		}
	}
	return ID
}

func recordDS18x20(kind, ID, model, value string) {
	fv, err := strconv.ParseFloat(value, 64)
	if err != nil {
		log.Printf("Error parsing sample value %q from device %q: %v", value, ID, err)
		return
	}
	// Convert back to celsius and round to nearest 0.1 degrees; the DS18S20 and
	// DS18B20 were both precise to within ±0.5°.
	fv = math.Round(10*(fv-32.)*5./9.) / 10.
	switch kind {
	case "temp":
		temperatureGauges.With(prometheus.Labels{
			"id":     ID,
			"device": formatDevice(ID, model),
			"model":  strings.ToLower(model),
		}).Set(fv)
	default:
		log.Printf("Unrecognized sensor type %q", kind)
	}
}

func recordDHT22(kind, model, v1, v2 string) {
	hv, err := strconv.ParseFloat(v1, 64)
	if err != nil {
		log.Printf("Error parsing sample value 1 %q from device %q: %v", v1, model, err)
		return
	}
	labels := prometheus.Labels{
		"id":     strings.ToLower(model),
		"device": strings.ToLower(model),
		"model":  strings.ToLower(model),
	}
	humidityGauges.With(labels).Set(hv)

	tv, err := strconv.ParseFloat(v2, 64)
	// For some reason past-me had this output in fahrenheit, and now can't
	// reflash to fix it; convert it back.  Round to 0.1 degrees, since the
	// DHT22 has a precision of ±0.5°C and reporting more is pointless.
	tv = math.Round(10*(tv-32.)*5./9.) / 10.
	if err != nil {
		log.Printf("Error parsing sample value 2 %q from device %q: %v", v2, model, err)
		return
	}
	temperatureGauges.With(labels).Set(tv)
}

func redial() {
	connectNum := 0
	for {
		seen := map[string]bool{}
		connectionAttempts.Inc()
		conn, err := net.DialTimeout("tcp", *connect, *connectTimeout)
		if err != nil {
			log.Printf("Error connecting to %s: %v", *connect, err)
			connectionErrors.Inc()
			time.Sleep(5 * time.Second)
			continue
		}
		connectNum++
		log.Printf("Connected to %s (connection %d)", *connect, connectNum)
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			t := scanner.Text()
			bytesReceived.Add(float64(len(t) + 1))
			if m := ds18x20SampleRE.FindStringSubmatch(t); m != nil {
				samplesReceived.Inc()
				if !seen[m[2]] {
					seen[m[2]] = true
					log.Printf("Got first sample from %s in connection %d", m[2], connectNum)
				}
				recordDS18x20(m[1], m[2], m[3], m[4])
			} else if m := dht22SampleRE.FindStringSubmatch(t); m != nil {
				samplesReceived.Inc()
				if !seen[m[2]] {
					seen[m[2]] = true
					log.Printf("Got first sample from %s in connection %d", m[2], connectNum)
				}
				recordDHT22(m[1], m[2], m[3], m[4])
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Read failed from %s: %v", *connect, err)
			conn.Close()
			time.Sleep(5 * time.Second)
		}
	}
}

func main() {
	flag.Parse()
	prometheus.MustRegister(connectionAttempts, connectionErrors,
		samplesReceived, bytesReceived, temperatureGauges, humidityGauges)
	go redial()
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*listen, nil))
}
