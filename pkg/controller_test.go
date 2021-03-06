package pkg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clientgo_testing "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

type testPod struct {
	pod     corev1.Pod
	metrics metricsv1beta1.PodMetrics
}

// Bellow are a list of predefined Pods which can be reused in different tests
var (
	// A Pod that has metrics but no definition
	podWithoutDefinition = testPod{
		pod: corev1.Pod{},
		metrics: metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-namespace",
			},
			Containers: []metricsv1beta1.ContainerMetrics{
				containerMetrics("container-1"),
			},
		},
	}

	// A Pod without limit that is using some memory
	podWithoutLimitUsingMemory = testPod{
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "test-namespace",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					container("container-1"),
				},
			},
		},
		metrics: metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "test-namespace",
			},
			Containers: []metricsv1beta1.ContainerMetrics{
				containerMetricsWithMemoryUsage("container-1", "1Gi"),
			},
		},
	}

	// A Pod with multiple containers, some of which having maxed out their memory
	podWithSomeContainersAboveRatio = testPod{
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-2",
				Namespace: "test-namespace",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					containerDefinitionWithMemoryLimit("container-1", "1Gi"),
					containerDefinitionWithMemoryLimit("container-2", "1Gi"),
					containerDefinitionWithMemoryLimit("container-3", "1Gi"),
					containerDefinitionWithMemoryLimit("container-4", "1Gi"),
				},
			},
		},
		metrics: metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-2",
				Namespace: "test-namespace",
			},
			Containers: []metricsv1beta1.ContainerMetrics{
				containerMetricsWithMemoryUsage("container-1", "1Gi"),
				containerMetricsWithMemoryUsage("container-2", "900Mi"),
				containerMetricsWithMemoryUsage("container-3", "400Mi"),
				containerMetricsWithMemoryUsage("container-4", "1Mi"),
			},
		},
	}

	// A Pod with multiple containers, all of which not close to their memory limit
	podWithAllContainersAlright = testPod{
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-3",
				Namespace: "test-namespace",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					containerDefinitionWithMemoryLimit("container-1", "1Gi"),
					containerDefinitionWithMemoryLimit("container-2", "1Gi"),
				},
			},
		},
		metrics: metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-3",
				Namespace: "test-namespace",
			},
			Containers: []metricsv1beta1.ContainerMetrics{
				containerMetricsWithMemoryUsage("container-1", "400Mi"),
				containerMetricsWithMemoryUsage("container-2", "400Mi"),
			},
		},
	}

	// A Pod with one container that is maxed out
	podSingleMaxedoutContainer = testPod{
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-4",
				Namespace: "test-namespace",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					containerDefinitionWithMemoryLimit("container-1", "1Gi"),
				},
			},
		},
		metrics: metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-3",
				Namespace: "test-namespace",
			},
			Containers: []metricsv1beta1.ContainerMetrics{
				containerMetricsWithMemoryUsage("container-1", "1Gi"),
			},
		},
	}
)

func TestContainerIdentificationFailsIfContainerDefinitionCantBeFound(t *testing.T) {
	_, err := identifyContainersCloseToMemoryLimit(context.Background(), podWithoutDefinition.metrics, podWithoutDefinition.pod, 60)
	assert.Error(t, err)
	assert.EqualError(t, err, "no container definition found for 'test-namespace/test-pod/container-1'")
}

func TestContainerIdentificationIgnoresContainersWithoutLimit(t *testing.T) {
	list, err := identifyContainersCloseToMemoryLimit(context.Background(), podWithoutLimitUsingMemory.metrics, podWithoutLimitUsingMemory.pod, 60)
	assert.NoError(t, err)
	assert.Empty(t, list)
}

func TestContainerIdentificationOnlyReturnsContainersAboveRatio(t *testing.T) {
	list, err := identifyContainersCloseToMemoryLimit(context.Background(), podWithSomeContainersAboveRatio.metrics, podWithSomeContainersAboveRatio.pod, 85)
	assert.NoError(t, err)
	assert.NotEmpty(t, list)
	assert.Equal(t, []string{"container-1", "container-2"}, list)
}

func TestEvictionOnlyAffectsPodsMaxingoutMemory(t *testing.T) {
	c := fakeController(podWithSomeContainersAboveRatio, podWithAllContainersAlright, podSingleMaxedoutContainer)

	err := c.evictPodsCloseToMemoryLimit(context.Background())
	assert.NoError(t, err)

	fakeClientSet := c.clientset.(*fake.Clientset)
	assert.Equal(t, 4, len(fakeClientSet.Actions()))

	action0 := fakeClientSet.Actions()[0]
	assert.Equal(t, "list", action0.GetVerb())
	assert.Equal(t, "/v1, Resource=pods", action0.GetResource().String())

	action1 := fakeClientSet.Actions()[1]
	assert.Equal(t, "watch", action1.GetVerb())
	assert.Equal(t, "/v1, Resource=pods", action1.GetResource().String())

	action2 := fakeClientSet.Actions()[2]
	assert.Equal(t, "create", action2.GetVerb())
	assert.Equal(t, "eviction", action2.GetSubresource())
	assert.Equal(t, "/v1, Resource=pods", action2.GetResource().String())

	obj2 := action2.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-2", obj2.(*policyv1beta1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj2.(*policyv1beta1.Eviction).Namespace)
	assert.Nil(t, obj2.(*policyv1beta1.Eviction).DeleteOptions)

	action3 := fakeClientSet.Actions()[3]
	assert.Equal(t, "create", action3.GetVerb())
	assert.Equal(t, "eviction", action3.GetSubresource())
	assert.Equal(t, "/v1, Resource=pods", action3.GetResource().String())

	obj3 := action3.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-3", obj3.(*policyv1beta1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj3.(*policyv1beta1.Eviction).Namespace)
	assert.Nil(t, obj3.(*policyv1beta1.Eviction).DeleteOptions)
}

func TestEvictionHasDryrunSet(t *testing.T) {
	c := fakeController(podWithSomeContainersAboveRatio, podWithAllContainersAlright, podSingleMaxedoutContainer)
	c.opts.DryRun = true

	err := c.evictPodsCloseToMemoryLimit(context.Background())
	assert.NoError(t, err)

	fakeClientSet := c.clientset.(*fake.Clientset)
	assert.Equal(t, 4, len(fakeClientSet.Actions()))

	action2 := fakeClientSet.Actions()[2]
	assert.Equal(t, "create", action2.GetVerb())
	assert.Equal(t, "eviction", action2.GetSubresource())

	obj2 := action2.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-2", obj2.(*policyv1beta1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj2.(*policyv1beta1.Eviction).Namespace)
	assert.Equal(t, 1, len(obj2.(*policyv1beta1.Eviction).DeleteOptions.DryRun))
	assert.Equal(t, "All", obj2.(*policyv1beta1.Eviction).DeleteOptions.DryRun[0])

	action3 := fakeClientSet.Actions()[3]
	assert.Equal(t, "create", action3.GetVerb())
	assert.Equal(t, "eviction", action3.GetSubresource())

	obj3 := action3.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-3", obj3.(*policyv1beta1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj3.(*policyv1beta1.Eviction).Namespace)
	assert.Equal(t, 1, len(obj3.(*policyv1beta1.Eviction).DeleteOptions.DryRun))
	assert.Equal(t, "All", obj3.(*policyv1beta1.Eviction).DeleteOptions.DryRun[0])
}

func fakeController(pairs ...testPod) *controller {
	podDefinitions := []runtime.Object{}
	podMetrics := []metricsv1beta1.PodMetrics{}
	for k := range pairs {
		podDefinitions = append(podDefinitions, &(pairs[k].pod))
		podMetrics = append(podMetrics, pairs[k].metrics)
	}

	clientset := fake.NewSimpleClientset(podDefinitions...)
	factory := informers.NewSharedInformerFactory(clientset, 0)
	lister := factory.Core().V1().Pods().Lister()

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	return &controller{
		clientset: clientset,
		lister:    lister,
		recorder:  record.NewFakeRecorder(10),

		// We can't use the official fake clientset for metrics because it's broken:
		// https://github.com/kubernetes/kubernetes/issues/95421
		//
		// This is why we're coming with our dummyPodMetricLister here.
		podMetrics: dummyPodMetricLister{Items: podMetrics},
		opts: Options{
			MemoryUsageThreshold: 90,
		},
	}
}

func containerDefinitionWithMemoryLimit(name string, memoryLimit string) corev1.Container {
	return corev1.Container{
		Name: name,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse(memoryLimit),
			},
		},
	}
}

func containerMetricsWithMemoryUsage(name string, memoryUsage string) metricsv1beta1.ContainerMetrics {
	return metricsv1beta1.ContainerMetrics{
		Name: name,
		Usage: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(memoryUsage),
		},
	}
}

func containerMetrics(name string) metricsv1beta1.ContainerMetrics {
	return metricsv1beta1.ContainerMetrics{
		Name: name,
	}
}

func container(name string) corev1.Container {
	return corev1.Container{
		Name: name,
	}
}

type dummyPodMetricLister struct {
	Items []metricsv1beta1.PodMetrics
}

func (p dummyPodMetricLister) List(ctx context.Context, opts metav1.ListOptions) (*metricsv1beta1.PodMetricsList, error) {
	return &metricsv1beta1.PodMetricsList{Items: p.Items}, nil
}
