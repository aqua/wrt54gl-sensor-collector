package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
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
	ds18x20SampleRE   = regexp.MustCompile(`^(?i)\d+ (temp) ([0-9a-f]+) (\w+) ([\d.]+)$`)
	dht22SampleRE     = regexp.MustCompile(`^(?i)\d+ (humidity) (DHT22) ([\d.]+) ([\d.]+)$`)
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
	fv = (fv - 32.) * 5. / 9.
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
	tv = (tv - 32.) * 5. / 9.
	if err != nil {
		log.Printf("Error parsing sample value 2 %q from device %q: %v", v2, model, err)
		return
	}
	temperatureGauges.With(labels).Set(tv)
}

func redial() {
	for {
		conn, err := net.DialTimeout("tcp", *connect, *connectTimeout)
		if err != nil {
			log.Printf("Error connecting to %s: %v", *connect, err)
			time.Sleep(5 * time.Second)
			continue
		}
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			t := scanner.Text()
			log.Printf("read: %s", t)
			bytesReceived.Add(float64(len(t) + 1))
			if m := ds18x20SampleRE.FindStringSubmatch(t); m != nil {
				samplesReceived.Inc()
				recordDS18x20(m[1], m[2], m[3], m[4])
			} else if m := dht22SampleRE.FindStringSubmatch(t); m != nil {
				samplesReceived.Inc()
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
	prometheus.MustRegister(samplesReceived, bytesReceived, temperatureGauges)
	go redial()
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*listen, nil))
}
