package pkg

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

const (
	metricsPath              = "/metrics"
	evictionMetricName       = "soft_pod_memory_evicter_evicted_pods_total"
	evictionMetricHelp       = "Total number of Pods evicted by soft-pod-memory-evicter."
	metricsShutdownGrace     = 5 * time.Second
	metricsReadHeaderTimeout = 4 * time.Second

	metricLabelNamespace   = "affected_namespace"
	metricLabelAppName     = "affected_app_kubernetes_io_name"
	metricLabelAppInstance = "affected_app_kubernetes_io_instance"
)

type metricsRecorder interface {
	Start(ctx context.Context)
	RecordPodEviction(pod *corev1.Pod)
	RecordObservedNamespace(pod *corev1.Pod)
}

func newMetricsRecorder(opts Options) metricsRecorder {
	if !opts.EnableMetrics {
		return noopMetricsRecorder{}
	}

	bindAddr := strings.TrimSpace(opts.MetricsBindAddress)
	if bindAddr == "" {
		klog.Warning("Metrics exporter enabled but no bind address provided; disabling exporter")
		return noopMetricsRecorder{}
	}

	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: evictionMetricName,
			Help: evictionMetricHelp,
		},
		[]string{
			metricLabelNamespace,
			metricLabelAppName,
			metricLabelAppInstance,
		},
	)

	registry := prometheus.NewRegistry()
	if err := registry.Register(counter); err != nil {
		klog.Errorf("unable to register metrics exporter: %v", err)
		return noopMetricsRecorder{}
	}

	mux := http.NewServeMux()
	mux.Handle(metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	return &prometheusMetricsRecorder{
		server: &http.Server{
			Addr:              bindAddr,
			Handler:           mux,
			ReadHeaderTimeout: metricsReadHeaderTimeout,
		},
		counter: counter,
	}
}

type noopMetricsRecorder struct{}

func (noopMetricsRecorder) Start(context.Context) {}

func (noopMetricsRecorder) RecordPodEviction(*corev1.Pod) {}

func (noopMetricsRecorder) RecordObservedNamespace(*corev1.Pod) {}

type prometheusMetricsRecorder struct {
	server  *http.Server
	counter *prometheus.CounterVec
	once    sync.Once
}

func (r *prometheusMetricsRecorder) Start(ctx context.Context) {
	r.once.Do(func() {
		go func() {
			<-ctx.Done()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), metricsShutdownGrace)
			defer cancel()

			if err := r.server.Shutdown(shutdownCtx); err != nil {
				klog.Errorf("unable to shutdown metrics exporter: %v", err)
			}
		}()

		go func() {
			klog.Infof("Prometheus exporter listening on %s%s", r.server.Addr, metricsPath)
			if err := r.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				klog.Errorf("Prometheus exporter failed: %v", err)
			}
		}()
	})
}

func (r *prometheusMetricsRecorder) RecordPodEviction(pod *corev1.Pod) {
	r.counter.WithLabelValues(
		pod.Namespace,
		pod.Labels[appKubernetesNameLabel],
		pod.Labels[appKubernetesInstanceLabel],
	).Inc()
}

func (r *prometheusMetricsRecorder) RecordObservedNamespace(pod *corev1.Pod) {
	r.counter.WithLabelValues(
		pod.Namespace,
		pod.Labels[appKubernetesNameLabel],
		pod.Labels[appKubernetesInstanceLabel],
	)
}
