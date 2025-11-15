package pkg

import (
	"time"

	"github.com/urfave/cli/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type SelectorFlag struct {
	Selector labels.Selector
}

func (selectorFlag *SelectorFlag) Set(value string) error {
	labelSelector, err := metav1.ParseToLabelSelector(value)
	if err != nil {
		return err
	}
	selector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return err
	}
	selectorFlag.Selector = selector
	return nil
}

func (selectorFlag *SelectorFlag) String() string {
	if selectorFlag.Selector == nil {
		return ""
	}
	return selectorFlag.Selector.String()
}

type Options struct {
	// DryRun=true won't evict Pods for real
	DryRun bool

	// PodSelector is the selector used to list pods, allowing inclusion or
	// exclusion of specific Pods.
	PodSelector SelectorFlag

	// MemoryUsageThreshold is the threshold (0-100) above which a Pod is considered
	// as overusing its memory.
	MemoryUsageThreshold int

	// EvictionPause is the delay we wait between two evictions, to prevent
	// removing too many Pods at once. Else we could more easily have downtimes if
	// Deployments don't specify a PodDisruptionBudget. Pods defining a
	// PodDisruptionBudget will ignore the pause, but respecting the budget.
	EvictionPause time.Duration

	// MemoryUsageCheckInterval is how often we check the memory usage.
	// It doesn't need to be too frequent, as we have to wait for the metric-server
	// to refresh the metrics all the time.
	MemoryUsageCheckInterval time.Duration

	// ChannelQueueSize is the size of the queue for Pods to evict.
	// It is filled each check interval and drained by the eviction loops. Eviction
	// pauses and backoffs cause the queue to fill up.
	ChannelQueueSize int

	// IgnoredNamespaces is a list of namespaces to ignore when checking Pods.
	IgnoredNamespaces cli.StringSlice

	// EnableMetrics toggles the Prometheus exporter.
	EnableMetrics bool

	// MetricsBindAddress is the bind address for the Prometheus exporter
	// (for example, ":9288").
	MetricsBindAddress string
}
