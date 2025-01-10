package pkg

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
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
	pdb     policyv1.PodDisruptionBudget
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

	// A pod with one container that is maxed out having a PodDisruptionBudget
	podSingleMaxedoutContainerWithPDB = testPod{
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-5",
				Namespace: "test-namespace",
				Labels: map[string]string{
					"app": "test-app",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					containerDefinitionWithMemoryLimit("container-1", "1Gi"),
				},
			},
		},
		metrics: metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-5",
				Namespace: "test-namespace",
			},
			Containers: []metricsv1beta1.ContainerMetrics{
				containerMetricsWithMemoryUsage("container-1", "1Gi"),
			},
		},
		pdb: policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-5-pdb",
				Namespace: "test-namespace",
			},
			Spec: policyv1.PodDisruptionBudgetSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "test-app",
					},
				},
			},
		},
	}
)

func TestContainerIdentificationFailsIfContainerDefinitionCantBeFound(t *testing.T) {
	_, err := identifyContainersCloseToMemoryLimit(podWithoutDefinition.metrics, podWithoutDefinition.pod, 60)
	assert.Error(t, err)
	assert.EqualError(t, err, "no container definition found for 'test-namespace/test-pod/container-1'")
}

func TestContainerIdentificationIgnoresContainersWithoutLimit(t *testing.T) {
	list, err := identifyContainersCloseToMemoryLimit(podWithoutLimitUsingMemory.metrics, podWithoutLimitUsingMemory.pod, 60)
	assert.NoError(t, err)
	assert.Empty(t, list)
}

func TestContainerIdentificationOnlyReturnsContainersAboveRatio(t *testing.T) {
	list, err := identifyContainersCloseToMemoryLimit(podWithSomeContainersAboveRatio.metrics, podWithSomeContainersAboveRatio.pod, 85)
	assert.NoError(t, err)
	assert.NotEmpty(t, list)
	assert.Equal(t, []string{"container-1", "container-2"}, list)
}

func TestEvictionOnlyAffectsPodsMaxingoutMemory(t *testing.T) {
	c := fakeController(podWithSomeContainersAboveRatio, podWithAllContainersAlright, podSingleMaxedoutContainer)

	err := c.evictPodsCloseToMemoryLimit(context.Background())
	assert.NoError(t, err)
	c.terminate_graceful()

	fakeClientSet := c.clientset.(*fake.Clientset)
	assert.Equal(t, 6, len(fakeClientSet.Actions()))

	first_four_actions := fakeClientSet.Actions()[:4]
	assertContainsAction(t, first_four_actions, "list", "pods")
	assertContainsAction(t, first_four_actions, "watch", "pods")
	assertContainsAction(t, first_four_actions, "list", "poddisruptionbudgets")
	assertContainsAction(t, first_four_actions, "watch", "poddisruptionbudgets")

	action4 := fakeClientSet.Actions()[4]
	assert.Equal(t, "create", action4.GetVerb())
	assert.Equal(t, "eviction", action4.GetSubresource())
	assert.Equal(t, "/v1, Resource=pods", action4.GetResource().String())

	obj2 := action4.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-2", obj2.(*policyv1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj2.(*policyv1.Eviction).Namespace)
	assert.Nil(t, obj2.(*policyv1.Eviction).DeleteOptions)

	action5 := fakeClientSet.Actions()[5]
	assert.Equal(t, "create", action5.GetVerb())
	assert.Equal(t, "eviction", action5.GetSubresource())
	assert.Equal(t, "/v1, Resource=pods", action5.GetResource().String())

	obj3 := action5.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-3", obj3.(*policyv1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj3.(*policyv1.Eviction).Namespace)
	assert.Nil(t, obj3.(*policyv1.Eviction).DeleteOptions)
}

func TestEvictionHasDryrunSet(t *testing.T) {
	c := fakeController(podWithSomeContainersAboveRatio, podWithAllContainersAlright, podSingleMaxedoutContainer)
	c.opts.DryRun = true

	err := c.evictPodsCloseToMemoryLimit(context.Background())
	assert.NoError(t, err)
	c.terminate_graceful()

	fakeClientSet := c.clientset.(*fake.Clientset)
	assert.Equal(t, 6, len(fakeClientSet.Actions()))

	first_four_actions := fakeClientSet.Actions()[:4]
	assertContainsAction(t, first_four_actions, "list", "pods")
	assertContainsAction(t, first_four_actions, "watch", "pods")
	assertContainsAction(t, first_four_actions, "list", "poddisruptionbudgets")
	assertContainsAction(t, first_four_actions, "watch", "poddisruptionbudgets")

	action4 := fakeClientSet.Actions()[4]
	assert.Equal(t, "create", action4.GetVerb())
	assert.Equal(t, "eviction", action4.GetSubresource())

	obj2 := action4.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-2", obj2.(*policyv1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj2.(*policyv1.Eviction).Namespace)
	assert.Equal(t, 1, len(obj2.(*policyv1.Eviction).DeleteOptions.DryRun))
	assert.Equal(t, "All", obj2.(*policyv1.Eviction).DeleteOptions.DryRun[0])

	action5 := fakeClientSet.Actions()[5]
	assert.Equal(t, "create", action5.GetVerb())
	assert.Equal(t, "eviction", action5.GetSubresource())

	obj3 := action5.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-3", obj3.(*policyv1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj3.(*policyv1.Eviction).Namespace)
	assert.Equal(t, 1, len(obj3.(*policyv1.Eviction).DeleteOptions.DryRun))
	assert.Equal(t, "All", obj3.(*policyv1.Eviction).DeleteOptions.DryRun[0])
}

func TestEvictionIgnoresNamespaces(t *testing.T) {
	c := fakeController(podSingleMaxedoutContainer)
	c.opts.IgnoredNamespaces = *cli.NewStringSlice("test-namespace")

	err := c.evictPodsCloseToMemoryLimit(context.Background())
	assert.NoError(t, err)
	c.terminate_graceful()

	fakeClientSet := c.clientset.(*fake.Clientset)
	assert.Equal(t, 4, len(fakeClientSet.Actions()))
	assertContainsAction(t, fakeClientSet.Actions(), "list", "pods")
	assertContainsAction(t, fakeClientSet.Actions(), "watch", "pods")
	assertContainsAction(t, fakeClientSet.Actions(), "list", "poddisruptionbudgets")
	assertContainsAction(t, fakeClientSet.Actions(), "watch", "poddisruptionbudgets")
}

func TestEvictionAlsoWorksForPodsWithDisruptionBudget(t *testing.T) {
	c := fakeController(podWithAllContainersAlright, podSingleMaxedoutContainerWithPDB)

	err := c.evictPodsCloseToMemoryLimit(context.Background())
	assert.NoError(t, err)
	c.terminate_graceful()

	fakeClientSet := c.clientset.(*fake.Clientset)
	assert.Equal(t, 5, len(fakeClientSet.Actions()))

	first_four_actions := fakeClientSet.Actions()[:4]
	assertContainsAction(t, first_four_actions, "list", "pods")
	assertContainsAction(t, first_four_actions, "watch", "pods")
	assertContainsAction(t, first_four_actions, "list", "poddisruptionbudgets")
	assertContainsAction(t, first_four_actions, "watch", "poddisruptionbudgets")

	action4 := fakeClientSet.Actions()[4]
	assert.Equal(t, "create", action4.GetVerb())
	assert.Equal(t, "eviction", action4.GetSubresource())
	assert.Equal(t, "/v1, Resource=pods", action4.GetResource().String())

	obj2 := action4.(clientgo_testing.CreateAction).GetObject()
	assert.Equal(t, "test-pod-5", obj2.(*policyv1.Eviction).Name)
	assert.Equal(t, "test-namespace", obj2.(*policyv1.Eviction).Namespace)
	assert.Nil(t, obj2.(*policyv1.Eviction).DeleteOptions)
}

func fakeController(podConfigs ...testPod) *controller {
	k8sObjects := []runtime.Object{}
	podMetrics := []metricsv1beta1.PodMetrics{}
	for k := range podConfigs {
		k8sObjects = append(k8sObjects, &(podConfigs[k].pod))
		podMetrics = append(podMetrics, podConfigs[k].metrics)
		if podConfigs[k].pdb.Name != "" {
			k8sObjects = append(k8sObjects, &(podConfigs[k].pdb))
		}
	}

	clientset := fake.NewSimpleClientset(k8sObjects...)
	factory := informers.NewSharedInformerFactory(clientset, 0)
	lister := factory.Core().V1().Pods().Lister()
	pdbLister := factory.Policy().V1().PodDisruptionBudgets().Lister()

	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	return &controller{
		clientset: clientset,
		lister:    lister,
		pdbLister: pdbLister,
		recorder:  record.NewFakeRecorder(10),
		pauseChan: make(chan *corev1.Pod, 10),
		pdbChan:   make(chan *corev1.Pod, 10),

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

func (c *controller) terminate_graceful() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		c.evictWithPauseChanLoop(context.Background())
		wg.Done()
	}()
	go func() {
		c.evictWithPDBChanLoop(context.Background())
		wg.Done()
	}()
	close(c.pauseChan)
	close(c.pdbChan)
	wg.Wait()
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

func assertContainsAction(t *testing.T, actions []clientgo_testing.Action, verb, resource string) bool {
	t.Helper()
	for _, action := range actions {
		if action.Matches(verb, resource) {
			return true
		}
	}
	t.Errorf("Action not found: %s %s", verb, resource)
	return false
}

type dummyPodMetricLister struct {
	Items []metricsv1beta1.PodMetrics
}

func (p dummyPodMetricLister) List(ctx context.Context, opts metav1.ListOptions) (*metricsv1beta1.PodMetricsList, error) {
	return &metricsv1beta1.PodMetricsList{Items: p.Items}, nil
}
