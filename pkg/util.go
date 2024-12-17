package pkg

import (
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func Configset() *rest.Config {
	// Try to load in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Errorf("Could not load in-cluster config: %v", err)

		// Fall back to local config
		config, err = clientcmd.BuildConfigFromFlags("", os.Getenv("HOME")+"/.kube/config")
		if err != nil {
			klog.Errorf("Failed to load client config: %v", err)
		}
	}
	return config
}
