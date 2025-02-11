package main

import (
	"flag"
	"github.com/fluepke/iperf3-exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"time"
)

const version = "1.2.1"
const namespace = "iperf3"

var (
	listenAddress      = flag.String("web.listen-address", ":9579", "Address to listen on for web interface and telemetry")
	logLevel           = flag.String("log.level", "info", "Logging level")
	iperf3Timeout      = flag.Duration("iperf3.timeout", 30*time.Second, "iperf3 timeout")
	iperf3Path         = flag.String("iperf3.path", "iperf3", "iper3 binary path")
	iperf3Duration     = flag.Duration("iperf3.time", 10*time.Second, "time in seconds to transmit for")
	iperf3OmitDuration = flag.Duration("iper3.omitTime", 5*time.Second, "Omit the first  n  seconds  of the test, to skip past the TCP slow-start period")
	iperf3Mss          = flag.Int("iperf3.mss", 1400, "Set TCP/SCTP maximum segment size (MTU - 40 bytes)")
	iperf3Reverse      = flag.Bool("iperf3.reverse", false, "Reverse the direction of a test, so that the server sends data to the client")
	iperf3Bandwidth    = flag.String("iperf3.bandwidth", "", "Bandwidth limit according to iperf3")
	iperf3Port         = flag.Uint("iperf3.port", 5201, "Port of the iperf3 server to use")

	iperf3DurationSummary = prometheus.NewSummary(prometheus.SummaryOpts{Name: prometheus.BuildFQName(namespace, "exporter", "duration_seconds"), Help: "Duration of collections by the iperf3 exporter."})
	iperf3Errors          = prometheus.NewCounter(prometheus.CounterOpts{Name: prometheus.BuildFQName(namespace, "exporter", "errors_total"), Help: "Errors raised by the iperf3 exporter."})
	iperf3BytesSent       = prometheus.NewGauge(prometheus.GaugeOpts{Name: prometheus.BuildFQName(namespace, "exporter", "sent_bytes"), Help: "Total bytes sent by iperf3."})
	iperf3BytesReceived   = prometheus.NewGauge(prometheus.GaugeOpts{Name: prometheus.BuildFQName(namespace, "exporter", "received_bytes"), Help: "Total bytes received by iperf3."})
)

func main() {
	flag.Parse()
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal("Invalid logging level")
	}
	log.SetLevel(level)

	log.WithFields(log.Fields{
		"author":  "@fluepke",
		"version": version,
	}).Info("Starting iperf3-exporter")

	prometheus.MustRegister(iperf3DurationSummary)
	prometheus.MustRegister(iperf3Errors)
	prometheus.MustRegister(iperf3BytesSent)
	prometheus.MustRegister(iperf3BytesReceived)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
            <head><title>iperf3-exporter</title></head>
			<body>
			<h1>iperf3-exporter</h1>
			<p>` + version + `</p>
			<form action="/probe" method="GET">
			<input type="text" name="target" value="target" />
			<input type="text" name="duration" value="5s" />
			</form>
			</body>
			</html>`))
	})

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", handleProbeRequest)
	log.WithFields(log.Fields{
		"listenAddress": *listenAddress,
	}).Info("Starting to listen")

	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func handleProbeRequest(w http.ResponseWriter, request *http.Request) {
	logger := log.WithFields(log.Fields{
		"uri":         request.RequestURI,
		"remote_addr": request.RemoteAddr,
	})

	logger.Debug("Received request")

	target := request.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "'target' parameter must be specified", http.StatusBadRequest)
		iperf3Errors.Inc()
		logger.Error("Target was not specified")
		return
	}

	var err error
	duration := request.URL.Query().Get("duration")
	testDuration := *iperf3Duration
	if duration != "" {
		testDuration, err = time.ParseDuration(duration)
		if err != nil {
			http.Error(w, "'duration' parameter must be duration", http.StatusBadRequest)
			iperf3Errors.Inc()
			logger.Error("'duration' parameter could not be parsed as duration")
			return
		}
	}

	omitDuration := request.URL.Query().Get("omit-duration")
	testOmitDuration := *iperf3OmitDuration
	if omitDuration != "" {
		testOmitDuration, err = time.ParseDuration(omitDuration)
		if err != nil {
			http.Error(w, "'omit-duration' parameter must be duration", http.StatusBadRequest)
			iperf3Errors.Inc()
			logger.Error("'omit-duration' parameter could not be parsed as duration")
			return
		}
	}

	mss := request.URL.Query().Get("mss")
	testMss := *iperf3Mss
	if mss != "" {
		testMss, err = strconv.Atoi(mss)
		if err != nil || testMss < 535 {
			http.Error(w, "'mss' parameter must be integer > 535", http.StatusBadRequest)
			iperf3Errors.Inc()
			logger.Error("'mss' parameter must be integer > 535")
			return
		}
	}
	if testMss < 535 {
		iperf3Errors.Inc()
		logger.Error("'mss' parameter must be integer > 535")
		return
	}

	reverse := request.URL.Query().Get("reverse")
	testReverse := *iperf3Reverse
	if reverse != "" {
		testReverse, err = strconv.ParseBool(reverse)
		if err != nil {
			http.Error(w, "'reverse' parameter must be bool", http.StatusBadRequest)
			iperf3Errors.Inc()
			logger.Error("'reverse' paramter could not be parsed as bool")
			return
		}
	}

	var testBandwidth *string
	if *iperf3Bandwidth != "" {
		testBandwidth = iperf3Bandwidth
	}

	bandwidth := request.URL.Query().Get("bandwidth")
	if bandwidth != "" {
		testBandwidth = &bandwidth
	}

	port := request.URL.Query().Get("port")
	testPort := *iperf3Port
	if port != "" {
		var uint64Port uint64
		uint64Port, err = strconv.ParseUint(port, 10, 16)
		testPort = uint(uint64Port)
		if err != nil {
			http.Error(w, "'port' parameter must be a port number <= 65535", http.StatusBadRequest)
			iperf3Errors.Inc()
			logger.Error("'port' parameter must be a port number <= 65535")
			return
		}
	}
	if testPort > 65535 {
		iperf3Errors.Inc()
		logger.Error("'port' parameter must be a port number <= 65535")
		return
	}

	iperf3Collector := &collector.Collector{
		Timeout:      *iperf3Timeout,
		Iperf3Path:   *iperf3Path,
		Target:       target,
		Duration:     testDuration,
		OmitDuration: testOmitDuration,
		MSS:          testMss,
		Reverse:      testReverse,
		Bandwidth:    testBandwidth,
		Port:         testPort,

		ErrorCounter: iperf3Errors,
		RxCounter:    iperf3BytesReceived,
		TxCounter:    iperf3BytesSent,
	}

	start := time.Now()
	registry := prometheus.NewRegistry()
	registry.MustRegister(iperf3Collector)
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, request)

	iperf3DurationSummary.Observe(time.Since(start).Seconds())
}
