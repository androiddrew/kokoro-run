package server

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	settings "github.com/androiddrew/kokoro-run/internal/config"
)

// metrics are the Prometheus metrics of one server. Each server has its own
// registry, so tests can run several servers in one process.
type metrics struct {
	requests   *prometheus.CounterVec   // model, format, status
	synthesis  *prometheus.HistogramVec // model
	rtf        *prometheus.HistogramVec // model
	firstAudio *prometheus.HistogramVec // model
	handler    http.Handler
}

// newMetrics registers every metric, with a zero series for the model and
// each supported format so dashboards and alerts see them before the first
// request.
func newMetrics(pool *Pool, formats []string) *metrics {
	reg := prometheus.NewRegistry()
	f := promauto.With(reg)
	histogram := func(name, help string, buckets []float64) *prometheus.HistogramVec {
		return f.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, []string{"model"})
	}
	m := &metrics{
		requests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "kokoro_requests_total",
			Help: "Speech requests by model, response format and status. Unknown models and formats have empty labels; 499 is a client that disconnected.",
		}, []string{"model", "format", "status"}),
		synthesis: histogram("kokoro_synthesis_seconds",
			"Time a successful request spent in the pipeline, excluding queue wait and encoding.",
			[]float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120}),
		rtf: histogram("kokoro_rtf",
			"Real-time factor of successful requests: synthesis seconds per second of audio.",
			[]float64{.05, .1, .2, .3, .4, .5, .6, .8, 1, 1.5, 2, 5}),
		firstAudio: histogram("kokoro_time_to_first_audio_seconds",
			"Time from a successful request's arrival to its first audio, including queue wait.",
			[]float64{.05, .1, .25, .5, .75, 1, 1.5, 2, 5, 10, 30}),
	}
	f.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "kokoro_queue_depth",
		Help: "Requests waiting for a replica.",
	}, func() float64 { return float64(pool.Waiting()) })
	f.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "kokoro_replicas_loaded",
		Help: "Pipelines loaded and serving requests.",
	}, func() float64 { return float64(pool.Loaded()) })
	name := settings.ModelName
	m.synthesis.WithLabelValues(name)
	m.rtf.WithLabelValues(name)
	m.firstAudio.WithLabelValues(name)
	for _, format := range formats {
		m.requests.WithLabelValues(name, format, strconv.Itoa(http.StatusOK))
	}
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m.handler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return m
}

// observe counts a finished speech request.
func (m *metrics) observe(rec *requestRecord) {
	m.requests.WithLabelValues(rec.modelLabel, rec.formatLabel, strconv.Itoa(rec.status)).Inc()
	if rec.status != http.StatusOK {
		return
	}
	m.synthesis.WithLabelValues(rec.model).Observe(rec.synthesis.Seconds())
	m.rtf.WithLabelValues(rec.model).Observe(rec.rtf())
	m.firstAudio.WithLabelValues(rec.model).Observe(rec.firstAudio.Seconds())
}
