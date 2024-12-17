package pkg

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1_typed "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1_lister "k8s.io/client-go/listers/core/v1"
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

	// MemoryUsageCheckInterval is how often we check the memory usage.
	// It doesn't need to be too frequent, as we have to wait for the metric-server
	// to refresh the metrics all the time.
	MemoryUsageCheckInterval time.Duration
}

type PodMetricsInterfaceList interface {
	List(ctx context.Context, opts metav1.ListOptions) (*metricsv1beta1.PodMetricsList, error)
}

type controller struct {
	lister     corev1_lister.PodLister
	podMetrics PodMetricsInterfaceList
	factory    informers.SharedInformerFactory
	clientset  kubernetes.Interface
	recorder   record.EventRecorder
	opts       Options
}

func NewController(opts Options) Controller {
	clientset, err := kubernetes.NewForConfig(Configset())
	if err != nil {
		klog.Fatalf("unable to initialize Kubernetes API Client: %s", err)
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	lister := factory.Core().V1().Pods().Lister()

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
		podMetrics: podMetrics,
		factory:    factory,
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
			return nil

		case <-ticker.C:
			err := c.evictPodsCloseToMemoryLimit(ctx)
			if err != nil {
				klog.Errorf("periodic check failed: %v", err)
			}
		}
	}
}

func (c *controller) evictPodsCloseToMemoryLimit(ctx context.Context) error {
	podList, err := c.listPodsCloseToMemoryLimit(ctx)
	if err != nil {
		return fmt.Errorf("error listing Pods to evict: %w", err)
	}
	for _, pod := range podList {
		evictionPolicy := policyv1beta1.Eviction{
			ObjectMeta: pod.ObjectMeta,
		}
		if c.opts.DryRun {
			evictionPolicy.DeleteOptions = &metav1.DeleteOptions{
				DryRun: []string{"All"},
			}
		}
		klog.V(1).Infof("Evicting Pod '%s/%s'", pod.Namespace, pod.Name)
		c.recorder.Event(pod.DeepCopyObject(), "Normal", "SoftEviction", fmt.Sprintf("Pod '%s/%s' has at least one container close to its memory limit", pod.Namespace, pod.Name))

		err := c.clientset.PolicyV1beta1().Evictions(pod.Namespace).Evict(ctx, &evictionPolicy)
		if err != nil {
			c.recorder.Event(pod.DeepCopyObject(), "Warning", "SoftEviction", fmt.Sprintf("Unable to evict Pod '%s/%s' : %v", pod.Namespace, pod.Name, err))
			klog.Errorf("error evicting '%s/%s': %v", pod.Namespace, pod.Name, err)
			continue
		}
	}
	return nil
}

func (c *controller) listPodsCloseToMemoryLimit(ctx context.Context) ([]*corev1.Pod, error) {
	klog.V(1).Info("Starting a new Pods memory usage check")

	podMetrics, err := c.podMetrics.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("unable to list Pod metrics: %w", err)
	}

	podsToBeEvicted := []*corev1.Pod{}
	for _, podMetric := range podMetrics.Items {
		klog.V(2).Infof("Checking Pod '%s/%s'", podMetric.Namespace, podMetric.Name)
		pod, err := c.lister.Pods(podMetric.Namespace).Get(podMetric.Name)
		if err != nil {
			klog.Errorf("Could not find Pod definition for '%s/%s'", podMetric.Namespace, podMetric.Name)
			continue
		}

		containers, err := identifyContainersCloseToMemoryLimit(ctx, podMetric, *pod, float64(c.opts.MemoryUsageThreshold))
		if err != nil {
			klog.Errorf("could not find Pod Container overusers '%s/%s'", podMetric.Namespace, podMetric.Name)
			continue
		}

		if len(containers) > 0 {
			podsToBeEvicted = append(podsToBeEvicted, pod)
		}
	}

	klog.V(2).Info("Pods memory usage check done")
	return podsToBeEvicted, nil
}

func identifyContainersCloseToMemoryLimit(ctx context.Context, podMetrics metricsv1beta1.PodMetrics, podDefinition corev1.Pod, usageMemoryUsageThresholdPercent float64) ([]string, error) {
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
