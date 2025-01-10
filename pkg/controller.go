package pkg

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1_typed "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1_lister "k8s.io/client-go/listers/core/v1"
	policyv1_lister "k8s.io/client-go/listers/policy/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metrics_client "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	// componentName is used when creating Kubernetes Events
	componentName = "soft-pod-memory-evicter"
)

type Controller interface {
	Run(ctx context.Context) error
}

type Options struct {
	// DryRun=true won't evict Pods for real
	DryRun bool

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
}

type PodMetricsInterfaceList interface {
	List(ctx context.Context, opts metav1.ListOptions) (*metricsv1beta1.PodMetricsList, error)
}

type controller struct {
	lister     corev1_lister.PodLister
	pdbLister  policyv1_lister.PodDisruptionBudgetLister
	podMetrics PodMetricsInterfaceList
	factory    informers.SharedInformerFactory
	clientset  kubernetes.Interface
	recorder   record.EventRecorder
	opts       Options
	pauseChan  chan *corev1.Pod // eviction channel for Pods without PodDisruptionBudget
	pdbChan    chan *corev1.Pod // eviction channel for Pods with PodDisruptionBudget
}

func NewController(opts Options) Controller {
	clientset, err := kubernetes.NewForConfig(Configset())
	if err != nil {
		klog.Fatalf("unable to initialize Kubernetes API Client: %s", err)
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	lister := factory.Core().V1().Pods().Lister()
	pdbLister := factory.Policy().V1().PodDisruptionBudgets().Lister()

	metricsClientset, err := metrics_client.NewForConfig(Configset())
	if err != nil {
		klog.Fatalf("unable to initialize Kubernetes Metrics Client: %s", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&corev1_typed.EventSinkImpl{Interface: clientset.CoreV1().Events(metav1.NamespaceAll)})

	podMetrics := metricsClientset.MetricsV1beta1().PodMetricses(metav1.NamespaceAll)
	return &controller{
		clientset:  clientset,
		opts:       opts,
		lister:     lister,
		pdbLister:  pdbLister,
		podMetrics: podMetrics,
		factory:    factory,
		pauseChan:  make(chan *corev1.Pod, opts.ChannelQueueSize),
		pdbChan:    make(chan *corev1.Pod, opts.ChannelQueueSize),
		recorder: eventBroadcaster.NewRecorder(
			scheme.Scheme,
			corev1.EventSource{Component: componentName},
		),
	}
}

func (c *controller) Run(ctx context.Context) error {
	stopCh := make(chan struct{})

	klog.V(1).Info("1/3 Starting Factory")
	c.factory.Start(stopCh)

	klog.V(1).Info("2/3 Syncing Factory Cache")
	c.factory.WaitForCacheSync(stopCh)

	klog.V(1).Info("3/3 Running initial check")
	go c.evictWithPauseChanLoop(ctx)
	go c.evictWithPDBChanLoop(ctx)
	err := c.evictPodsCloseToMemoryLimit(ctx)
	if err != nil {
		return fmt.Errorf("initial check failed: %w", err)
	}
	klog.V(0).Info("Controller is ready!")

	ticker := time.NewTicker(c.opts.MemoryUsageCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.V(0).Info("Context was cancelled, shutting down Controller!")
			close(stopCh)
			close(c.pauseChan)
			close(c.pdbChan)
			return nil

		case <-ticker.C:
			err := c.evictPodsCloseToMemoryLimit(ctx)
			if err != nil {
				klog.Errorf("periodic check failed: %v", err)
			}
		}
	}
}

// evict pods with a pause between each eviction
func (c *controller) evictWithPauseChanLoop(ctx context.Context) {
	for pod := range c.pauseChan {
		err := c.evictPod(ctx, pod)
		if err != nil {
			c.recorder.Event(pod.DeepCopyObject(), "Warning", "SoftEviction", fmt.Sprintf("Unable to evict Pod '%s/%s' : %v", pod.Namespace, pod.Name, err))
			klog.Errorf("error evicting '%s/%s': %v", pod.Namespace, pod.Name, err)
			continue
		}

		if c.opts.EvictionPause > 0 {
			klog.V(2).Infof("Pausing pod eviction for %s", c.opts.EvictionPause)
			time.Sleep(c.opts.EvictionPause)
		}

		klog.V(2).Infof("Pod '%s/%s' evicted", pod.Namespace, pod.Name)
	}
}

// evict pods having a PodDisruptionBudget. If the PDB is exceeded, the eviction
// is retried after a delay using a different channel.
func (c *controller) evictWithPDBChanLoop(ctx context.Context) {
	for pod := range c.pdbChan {
		err := c.evictPod(ctx, pod)
		if err != nil {
			if strings.Contains(err.Error(), "disruption budget") {
				klog.V(2).Infof("Disruption budget exceeded. Skipping '%s/%s' this interval.", pod.Namespace, pod.Name)
				continue
			}

			c.recorder.Event(pod.DeepCopyObject(), "Warning", "SoftEviction", fmt.Sprintf("Unable to evict Pod '%s/%s' : %v", pod.Namespace, pod.Name, err))
			klog.Errorf("error evicting '%s/%s': %v", pod.Namespace, pod.Name, err)
			continue
		}

		klog.V(2).Infof("Pod '%s/%s' evicted", pod.Namespace, pod.Name)
	}
}

func (c *controller) schedulePodEviction(pod *corev1.Pod) {
	klog.V(1).Infof("Scheduling eviction for Pod '%s/%s'", pod.Namespace, pod.Name)
	c.recorder.Event(pod.DeepCopyObject(), "Normal", "SoftEviction", fmt.Sprintf("Pod '%s/%s' has at least one container close to its memory limit", pod.Namespace, pod.Name))

	if c.hasDisruptionBudget(pod) {
		if len(c.pdbChan) == cap(c.pdbChan) {
			klog.Warningf("PDB channel is full, skipping eviction for Pod '%s/%s'", pod.Namespace, pod.Name)
		} else {
			c.pdbChan <- pod
		}
	} else {
		if len(c.pauseChan) == cap(c.pauseChan) {
			klog.Warningf("Pause channel is full, skipping eviction for Pod '%s/%s'", pod.Namespace, pod.Name)
		} else {
			c.pauseChan <- pod
		}
	}
}

func (c *controller) evictPod(ctx context.Context, pod *corev1.Pod) error {
	evictionPolicy := policyv1.Eviction{
		ObjectMeta: pod.ObjectMeta,
	}
	if c.opts.DryRun {
		evictionPolicy.DeleteOptions = &metav1.DeleteOptions{
			DryRun: []string{"All"},
		}
	}

	return c.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &evictionPolicy)
}

func (c *controller) evictPodsCloseToMemoryLimit(ctx context.Context) error {
	klog.V(1).Info("Starting a new Pods memory usage check")

	podMetrics, err := c.podMetrics.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to list Pod metrics: %w", err)
	}

	for _, podMetric := range podMetrics.Items {
		klog.V(2).Infof("Checking Pod '%s/%s'", podMetric.Namespace, podMetric.Name)
		if slices.Contains(c.opts.IgnoredNamespaces.Value(), podMetric.Namespace) {
			klog.V(2).Infof("Pod '%s/%s' is in an ignored namespace, skipping", podMetric.Namespace, podMetric.Name)
			continue
		}

		pod, err := c.lister.Pods(podMetric.Namespace).Get(podMetric.Name)
		if err != nil {
			klog.Errorf("Could not find Pod definition for '%s/%s'", podMetric.Namespace, podMetric.Name)
			continue
		}

		containers, err := identifyContainersCloseToMemoryLimit(podMetric, *pod, float64(c.opts.MemoryUsageThreshold))
		if err != nil {
			klog.Errorf("could not find Pod Container overusers '%s/%s'", podMetric.Namespace, podMetric.Name)
			continue
		}

		if len(containers) > 0 {
			c.schedulePodEviction(pod)
		}
	}

	klog.V(2).Info("Pods memory usage check done")
	return nil
}

func identifyContainersCloseToMemoryLimit(podMetrics metricsv1beta1.PodMetrics, podDefinition corev1.Pod, usageMemoryUsageThresholdPercent float64) ([]string, error) {
	containerOverConsuming := []string{}
	for _, containerMetric := range podMetrics.Containers {
		klog.V(2).Infof("Analyzing memory usage of container '%s/%s/%s'", podMetrics.Namespace, podMetrics.Name, containerMetric.Name)

		var containerDefinition *corev1.Container
		for _, containerDef := range podDefinition.Spec.Containers {
			if containerDef.Name == containerMetric.Name {
				containerDefinition = &containerDef
				break
			}
		}
		if containerDefinition == nil {
			return nil, fmt.Errorf("no container definition found for '%s/%s/%s'", podMetrics.Namespace, podMetrics.Name, containerMetric.Name)
		}

		if containerDefinition.Resources.Limits == nil {
			klog.V(3).Infof("No limits found for container '%s/%s/%s'", podMetrics.Namespace, podMetrics.Name, containerMetric.Name)
			continue
		} else if containerDefinition.Resources.Limits.Memory() == nil {
			klog.V(3).Infof("No memory limit found for container '%s/%s/%s'", podMetrics.Namespace, podMetrics.Name, containerMetric.Name)
			continue
		} else if containerDefinition.Resources.Limits.Memory().IsZero() {
			klog.V(3).Infof("Memory limit is 0 for container '%s/%s/%s'", podMetrics.Namespace, podMetrics.Name, containerMetric.Name)
			continue
		}
		memoryUsagePercent := 100 * containerMetric.Usage.Memory().AsApproximateFloat64() / containerDefinition.Resources.Limits.Memory().AsApproximateFloat64()
		klog.V(1).Infof("Memory usage for '%s/%s/%s' is %.1f%%\n", podMetrics.Namespace, podMetrics.Name, containerMetric.Name, memoryUsagePercent)

		if memoryUsagePercent > usageMemoryUsageThresholdPercent {
			containerOverConsuming = append(containerOverConsuming, containerMetric.Name)
		}
	}
	return containerOverConsuming, nil
}

func (c *controller) hasDisruptionBudget(pod *corev1.Pod) bool {
	pdbList, err := c.pdbLister.PodDisruptionBudgets(pod.Namespace).List(labels.Everything())
	if err != nil {
		klog.Errorf("unable to list PodDisruptionBudgets: %s", err)
		return false
	}

	for _, pdb := range pdbList {
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			klog.Errorf("unable to convert label selector: %s", err)
			return false
		}

		if selector.Matches(labels.Set(pod.Labels)) {
			return true
		}
	}

	return false
}
