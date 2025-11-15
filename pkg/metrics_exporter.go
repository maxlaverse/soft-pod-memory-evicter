package pkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	metricsPath          = "/metrics"
	evictionMetricName   = "soft_pod_memory_evicter_evicted_pods_total"
	evictionMetricHelp   = "Total number of Pods evicted by soft-pod-memory-evicter."
	metricsShutdownGrace = 5 * time.Second
)

type metricsRecorder interface {
	Start(ctx context.Context)
	RecordPodEviction(namespace, appName, appInstance string)
	RecordObservedNamespace(namespace, appName, appInstance string)
}

func newMetricsRecorder(opts Options) metricsRecorder {
	if !opts.EnableMetrics {
		return noopMetricsRecorder{}
	}

	if strings.TrimSpace(opts.MetricsBindAddress) == "" {
		klog.Warning("Metrics exporter enabled but no bind address provided; disabling exporter")
		return noopMetricsRecorder{}
	}

	recorder := &prometheusMetricsRecorder{
		counter: newEvictedPodCounter(),
	}
	mux := http.NewServeMux()
	mux.Handle(metricsPath, http.HandlerFunc(recorder.handleMetrics))
	recorder.server = &http.Server{
		Addr:              opts.MetricsBindAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return recorder
}

type noopMetricsRecorder struct{}

func (noopMetricsRecorder) Start(context.Context) {}

func (noopMetricsRecorder) RecordPodEviction(_, _, _ string) {}

func (noopMetricsRecorder) RecordObservedNamespace(_, _, _ string) {}

type prometheusMetricsRecorder struct {
	server  *http.Server
	counter *evictedPodCounter
	once    sync.Once
}

func (r *prometheusMetricsRecorder) Start(ctx context.Context) {
	if r.server == nil {
		return
	}

	r.once.Do(func() {
		go func() {
			<-ctx.Done()

			shutdownCtx, cancel := context.WithTimeout(context.Background(), metricsShutdownGrace)
			defer cancel()

			if err := r.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

func (r *prometheusMetricsRecorder) RecordPodEviction(namespace, appName, appInstance string) {
	if r.counter == nil {
		return
	}

	r.counter.Inc(namespace, appName, appInstance)
}

func (r *prometheusMetricsRecorder) RecordObservedNamespace(namespace, appName, appInstance string) {
	if r.counter == nil {
		return
	}

	r.counter.EnsureMetric(namespace, appName, appInstance)
}

func (r *prometheusMetricsRecorder) handleMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Header().Set("Cache-Control", "no-store")

	// HEAD only needs headers
	if req.Method == http.MethodHead {
		return
	}

	if err := writeMetrics(w, r.counter); err != nil {
		klog.Errorf("unable to write metrics response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func writeMetrics(w io.Writer, counter *evictedPodCounter) error {
	if counter == nil {
		return nil
	}

	if _, err := fmt.Fprintf(w, "# HELP %s %s\n", evictionMetricName, evictionMetricHelp); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "# TYPE %s counter\n", evictionMetricName); err != nil {
		return err
	}

	return counter.WriteTo(w)
}

type evictedPodCounter struct {
	mu     sync.RWMutex
	counts map[metricKey]uint64
}

type metricKey struct {
	namespace string
	appName   string
	appInst   string
}

type metricEntry struct {
	key   metricKey
	value uint64
}

func newEvictedPodCounter() *evictedPodCounter {
	return &evictedPodCounter{
		counts: make(map[metricKey]uint64),
	}
}

func (c *evictedPodCounter) Inc(namespace, appName, appInstance string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := metricKey{namespace: namespace, appName: appName, appInst: appInstance}
	c.counts[key]++
}

func (c *evictedPodCounter) EnsureMetric(namespace, appName, appInstance string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := metricKey{namespace: namespace, appName: appName, appInst: appInstance}
	if _, ok := c.counts[key]; !ok {
		c.counts[key] = 0
	}
}

func (c *evictedPodCounter) snapshot() []metricEntry {
	c.mu.RLock()
	entries := make([]metricEntry, 0, len(c.counts))
	for key, value := range c.counts {
		entries = append(entries, metricEntry{
			key:   key,
			value: value,
		})
	}
	c.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].key.namespace == entries[j].key.namespace {
			if entries[i].key.appName == entries[j].key.appName {
				return entries[i].key.appInst < entries[j].key.appInst
			}
			return entries[i].key.appName < entries[j].key.appName
		}
		return entries[i].key.namespace < entries[j].key.namespace
	})

	return entries
}

func (c *evictedPodCounter) WriteTo(w io.Writer) error {
	for _, entry := range c.snapshot() {
			if _, err := fmt.Fprintf(
				w,
				"%s{affected_namespace=\"%s\",affected_app_kubernetes_io_name=\"%s\",affected_app_kubernetes_io_instance=\"%s\"} %d\n",
				evictionMetricName,
				escapeLabelValue(entry.key.namespace),
				escapeLabelValue(entry.key.appName),
				escapeLabelValue(entry.key.appInst),
				entry.value,
			); err != nil {
			return err
		}
	}

	return nil
}

func escapeLabelValue(source string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\n", "\\n",
		"\"", "\\\"",
	)
	return replacer.Replace(source)
}
