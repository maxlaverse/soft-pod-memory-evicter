package pkg

import (
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
			assert.Equal(t, metricsReadHeaderTimeout, r.server.ReadHeaderTimeout)
		}
		assert.NotNil(t, r.counter)
	})
}

func TestPrometheusMetricsRecorder_RecordsEvictions(t *testing.T) {
	recorder, ok := newMetricsRecorder(Options{
		EnableMetrics:      true,
		MetricsBindAddress: ":0",
	}).(*prometheusMetricsRecorder)
	if !ok {
		t.Fatalf("expected prometheusMetricsRecorder")
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "alpha",
			Labels: map[string]string{
				appKubernetesNameLabel:     "api",
				appKubernetesInstanceLabel: "prod",
			},
		},
	}

	recorder.RecordObservedNamespace(pod)
	recorder.RecordPodEviction(pod)

	req := httptest.NewRequest(http.MethodGet, metricsPath, nil)
	rr := httptest.NewRecorder()

	recorder.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(
		t,
		rr.Body.String(),
		`soft_pod_memory_evicter_evicted_pods_total{affected_app_kubernetes_io_instance="prod",affected_app_kubernetes_io_name="api",affected_namespace="alpha"} 1`,
	)
}
