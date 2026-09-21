package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	Registry        *prometheus.Registry
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	TTFT            *prometheus.HistogramVec
	InputTokens     *prometheus.CounterVec
	OutputTokens    *prometheus.CounterVec
	ProviderErrors  *prometheus.CounterVec
	PluginHealth    *prometheus.GaugeVec
	ActiveStreams   prometheus.Gauge
}

func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{Registry: reg}
	m.RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "providerapi_requests_total",
		Help: "Total completion requests",
	}, []string{"provider", "model", "status"})
	m.RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "providerapi_request_duration_seconds",
		Help:    "End-to-end request duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider", "model"})
	m.TTFT = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "providerapi_ttft_seconds",
		Help:    "Time to first token",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider", "model"})
	m.InputTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "providerapi_input_tokens_total",
		Help: "Input tokens",
	}, []string{"provider", "model"})
	m.OutputTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "providerapi_output_tokens_total",
		Help: "Output tokens",
	}, []string{"provider", "model"})
	m.ProviderErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "providerapi_provider_errors_total",
		Help: "Provider errors",
	}, []string{"provider", "code"})
	m.PluginHealth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "providerapi_plugin_health",
		Help: "Plugin health (1=healthy)",
	}, []string{"provider"})
	m.ActiveStreams = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "providerapi_active_streams",
		Help: "In-flight streams",
	})
	reg.MustRegister(
		m.RequestsTotal, m.RequestDuration, m.TTFT, m.InputTokens, m.OutputTokens,
		m.ProviderErrors, m.PluginHealth, m.ActiveStreams,
	)
	return m
}
