package pkg

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewMetricsRecorder(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		recorder := newMetricsRecorder(Options{
			EnableMetrics:      false,
			MetricsBindAddress: ":1234",
		})

		assert.IsType(t, noopMetricsRecorder{}, recorder)
	})

	t.Run("missing_address", func(t *testing.T) {
		recorder := newMetricsRecorder(Options{
			EnableMetrics:      true,
			MetricsBindAddress: "   ",
		})

		assert.IsType(t, noopMetricsRecorder{}, recorder)
	})

	t.Run("valid_address", func(t *testing.T) {
		rec := newMetricsRecorder(Options{
			EnableMetrics:      true,
			MetricsBindAddress: ":1234",
		})

		assert.IsType(t, &prometheusMetricsRecorder{}, rec)
		r, ok := rec.(*prometheusMetricsRecorder)
		if !ok {
			return
		}

		if assert.NotNil(t, r.server) {
			assert.Equal(t, ":1234", r.server.Addr)
		}
		assert.NotNil(t, r.counter)
	})
}

func TestWriteMetrics(t *testing.T) {
	counter := newEvictedPodCounter()
	counter.Inc("alpha", "api", "prod")
	counter.Inc("alpha", "api", "prod")
	counter.Inc(`ns"quote`, `app\name`, "inst/1")
	counter.EnsureMetric("beta", "", "")

	var buf bytes.Buffer
	err := writeMetrics(&buf, counter)
	assert.NoError(t, err)

	expected := "" +
		"# HELP " + evictionMetricName + " " + evictionMetricHelp + "\n" +
		"# TYPE " + evictionMetricName + " counter\n" +
		evictionMetricName + `{affected_namespace="alpha",affected_app_kubernetes_io_name="api",affected_app_kubernetes_io_instance="prod"} 2` + "\n" +
		evictionMetricName + `{affected_namespace="beta",affected_app_kubernetes_io_name="",affected_app_kubernetes_io_instance=""} 0` + "\n" +
		evictionMetricName + `{affected_namespace="ns\"quote",affected_app_kubernetes_io_name="app\\name",affected_app_kubernetes_io_instance="inst/1"} 1` + "\n"

	assert.Equal(t, expected, buf.String())
}

func TestPrometheusMetricsHandler(t *testing.T) {
	recorder := &prometheusMetricsRecorder{
		counter: newEvictedPodCounter(),
	}
	recorder.counter.Inc("default", "api", "prod")

	t.Run("GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, metricsPath, nil)
		rr := httptest.NewRecorder()

		recorder.handleMetrics(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "text/plain; version=0.0.4", rr.Header().Get("Content-Type"))
		assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
		assert.NotEmpty(t, rr.Body.Bytes())
	})

	t.Run("HEAD", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, metricsPath, nil)
		rr := httptest.NewRecorder()

		recorder.handleMetrics(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, 0, rr.Body.Len())
	})

	t.Run("invalid_method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, metricsPath, nil)
		rr := httptest.NewRecorder()

		recorder.handleMetrics(rr, req)

		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})
}
