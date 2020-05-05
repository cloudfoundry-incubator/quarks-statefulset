/*

Don't alter this file, it was generated.

*/
// Code generated by client-gen. DO NOT EDIT.

package versioned

import (
	"fmt"

	boshdeploymentv1alpha1 "code.cloudfoundry.org/quarks-operator/pkg/kube/client/clientset/versioned/typed/boshdeployment/v1alpha1"
	quarkssecretv1alpha1 "code.cloudfoundry.org/quarks-operator/pkg/kube/client/clientset/versioned/typed/quarkssecret/v1alpha1"
	quarksstatefulsetv1alpha1 "code.cloudfoundry.org/quarks-operator/pkg/kube/client/clientset/versioned/typed/quarksstatefulset/v1alpha1"
	discovery "k8s.io/client-go/discovery"
	rest "k8s.io/client-go/rest"
	flowcontrol "k8s.io/client-go/util/flowcontrol"
)

type Interface interface {
	Discovery() discovery.DiscoveryInterface
	BoshdeploymentV1alpha1() boshdeploymentv1alpha1.BoshdeploymentV1alpha1Interface
	QuarkssecretV1alpha1() quarkssecretv1alpha1.QuarkssecretV1alpha1Interface
	QuarksstatefulsetV1alpha1() quarksstatefulsetv1alpha1.QuarksstatefulsetV1alpha1Interface
}

// Clientset contains the clients for groups. Each group has exactly one
// version included in a Clientset.
type Clientset struct {
	*discovery.DiscoveryClient
	boshdeploymentV1alpha1    *boshdeploymentv1alpha1.BoshdeploymentV1alpha1Client
	quarkssecretV1alpha1      *quarkssecretv1alpha1.QuarkssecretV1alpha1Client
	quarksstatefulsetV1alpha1 *quarksstatefulsetv1alpha1.QuarksstatefulsetV1alpha1Client
}

// BoshdeploymentV1alpha1 retrieves the BoshdeploymentV1alpha1Client
func (c *Clientset) BoshdeploymentV1alpha1() boshdeploymentv1alpha1.BoshdeploymentV1alpha1Interface {
	return c.boshdeploymentV1alpha1
}

// QuarkssecretV1alpha1 retrieves the QuarkssecretV1alpha1Client
func (c *Clientset) QuarkssecretV1alpha1() quarkssecretv1alpha1.QuarkssecretV1alpha1Interface {
	return c.quarkssecretV1alpha1
}

// QuarksstatefulsetV1alpha1 retrieves the QuarksstatefulsetV1alpha1Client
func (c *Clientset) QuarksstatefulsetV1alpha1() quarksstatefulsetv1alpha1.QuarksstatefulsetV1alpha1Interface {
	return c.quarksstatefulsetV1alpha1
}

// Discovery retrieves the DiscoveryClient
func (c *Clientset) Discovery() discovery.DiscoveryInterface {
	if c == nil {
		return nil
	}
	return c.DiscoveryClient
}

// NewForConfig creates a new Clientset for the given config.
// If config's RateLimiter is not set and QPS and Burst are acceptable,
// NewForConfig will generate a rate-limiter in configShallowCopy.
func NewForConfig(c *rest.Config) (*Clientset, error) {
	configShallowCopy := *c
	if configShallowCopy.RateLimiter == nil && configShallowCopy.QPS > 0 {
		if configShallowCopy.Burst <= 0 {
			return nil, fmt.Errorf("burst is required to be greater than 0 when RateLimiter is not set and QPS is set to greater than 0")
		}
		configShallowCopy.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(configShallowCopy.QPS, configShallowCopy.Burst)
	}
	var cs Clientset
	var err error
	cs.boshdeploymentV1alpha1, err = boshdeploymentv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.quarkssecretV1alpha1, err = quarkssecretv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.quarksstatefulsetV1alpha1, err = quarksstatefulsetv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}

	cs.DiscoveryClient, err = discovery.NewDiscoveryClientForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

// NewForConfigOrDie creates a new Clientset for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *Clientset {
	var cs Clientset
	cs.boshdeploymentV1alpha1 = boshdeploymentv1alpha1.NewForConfigOrDie(c)
	cs.quarkssecretV1alpha1 = quarkssecretv1alpha1.NewForConfigOrDie(c)
	cs.quarksstatefulsetV1alpha1 = quarksstatefulsetv1alpha1.NewForConfigOrDie(c)

	cs.DiscoveryClient = discovery.NewDiscoveryClientForConfigOrDie(c)
	return &cs
}

// New creates a new Clientset for the given RESTClient.
func New(c rest.Interface) *Clientset {
	var cs Clientset
	cs.boshdeploymentV1alpha1 = boshdeploymentv1alpha1.New(c)
	cs.quarkssecretV1alpha1 = quarkssecretv1alpha1.New(c)
	cs.quarksstatefulsetV1alpha1 = quarksstatefulsetv1alpha1.New(c)

	cs.DiscoveryClient = discovery.NewDiscoveryClient(c)
	return &cs
}
