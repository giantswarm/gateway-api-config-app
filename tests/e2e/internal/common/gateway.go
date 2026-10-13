// Package common holds the setup and lookups the e2e suites share. It is not a
// suite itself: the suites live under suites/ and import it.
package common

import (
	"fmt"

	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/client"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ProxyNamespace is where Envoy Gateway runs the proxy pods and Services.
	ProxyNamespace = "envoy-gateway-system"
	// DefaultGatewayName is the name of the chart's default Gateway.
	DefaultGatewayName = "giantswarm-default"
	// DefaultGatewayClassName is the name of the chart's default GatewayClass.
	DefaultGatewayClassName = "giantswarm-default"
	// ZoneTopologyKey is the topology key of the zone spread constraint the chart sets
	// on the proxy pods of an AWS NLB gateway.
	ZoneTopologyKey = "topology.kubernetes.io/zone"
)

// DefaultGatewayProxySelector selects the proxy pods and Service of the default
// gateway by the owning-gateway labels Envoy Gateway stamps on them.
var DefaultGatewayProxySelector = labels.SelectorFromSet(map[string]string{
	"gateway.envoyproxy.io/owning-gateway-name":      DefaultGatewayName,
	"gateway.envoyproxy.io/owning-gateway-namespace": ProxyNamespace,
})

// WC returns a client for the workload cluster under test.
func WC() *client.Client {
	wcClient, err := state.GetFramework().WC(state.GetCluster().Name)
	Expect(err).NotTo(HaveOccurred())
	return wcClient
}

// GatewayProxyPods returns the envoy proxy pods of the default gateway.
// It errors when none are found so it can be used inside an Eventually block.
func GatewayProxyPods(wcClient *client.Client) (*corev1.PodList, error) {
	proxyPods := &corev1.PodList{}
	err := wcClient.List(state.GetContext(), proxyPods, &cr.ListOptions{
		Namespace:     ProxyNamespace,
		LabelSelector: DefaultGatewayProxySelector,
	})
	if err != nil {
		return nil, err
	}
	if len(proxyPods.Items) == 0 {
		return nil, fmt.Errorf("no envoy proxy pods found for gateway %s", DefaultGatewayName)
	}
	return proxyPods, nil
}

// ReadyWorkerNodes returns the Ready nodes that are not control plane nodes.
func ReadyWorkerNodes(wcClient *client.Client) ([]corev1.Node, error) {
	nodes := &corev1.NodeList{}
	if err := wcClient.List(state.GetContext(), nodes, client.DoesNotHaveLabels{"node-role.kubernetes.io/control-plane"}); err != nil {
		return nil, err
	}

	ready := []corev1.Node{}
	for _, node := range nodes.Items {
		if IsNodeReady(node) {
			ready = append(ready, node)
		}
	}
	return ready, nil
}

// IsNodeReady reports whether the node's Ready condition is True.
func IsNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
