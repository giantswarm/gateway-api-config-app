package basic

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/client"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// doNotDisruptAnnotation tells Karpenter not to voluntarily drain the node a
	// pod runs on. The chart must not set it on the proxy pods: it stalls
	// consolidation and node rollouts.
	doNotDisruptAnnotation = "karpenter.sh/do-not-disrupt"
	// karpenterNodePoolLabel is only present on nodes Karpenter provisioned, so it
	// distinguishes them from the ASG-backed nodes of a regular node pool.
	karpenterNodePoolLabel = "karpenter.sh/nodepool"
	// hostnameTopologyKey is the topology key of the one-proxy-per-node
	// anti-affinity term the chart sets for AWS NLB gateways.
	hostnameTopologyKey = "kubernetes.io/hostname"
	// expectedTerminationGracePeriod must stay above the 200s shutdown.drainTimeout
	// so envoy finishes draining before the kubelet kills it.
	expectedTerminationGracePeriod int64 = 240
)

// gatewayKarpenterNodeTests asserts that the Karpenter node pool declared in the
// test cluster values actually provisioned nodes. Without it the proxy pod
// assertions below would pass on an ASG-only cluster and prove nothing.
func gatewayKarpenterNodeTests() {
	wcClient, _ := state.GetFramework().WC(state.GetCluster().Name)

	// Waiting 20 minutes for nodes that can never appear is the wrong failure. A
	// cluster built without the Karpenter node pool, which is what happens when the
	// suite runs against a pre-built cluster (E2E_WC_NAME/E2E_WC_NAMESPACE) instead
	// of standing one up from test_data/cluster_values.yaml, has no NodePool at all.
	By("checking the cluster declares a Karpenter node pool")
	nodePools := &unstructured.UnstructuredList{}
	nodePools.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "karpenter.sh",
		Version: "v1",
		Kind:    "NodePoolList",
	})
	err := wcClient.List(state.GetContext(), nodePools)
	Expect(err).NotTo(HaveOccurred(),
		"cannot list Karpenter NodePools, so Karpenter is not installed on this cluster")
	Expect(nodePools.Items).NotTo(BeEmpty(),
		"the cluster has no Karpenter NodePool, check the karpenter node pool in test_data/cluster_values.yaml was applied")

	By("checking the cluster has Karpenter-provisioned nodes")
	Eventually(func() error {
		nodes := &corev1.NodeList{}
		if err := wcClient.List(state.GetContext(), nodes, cr.HasLabels{karpenterNodePoolLabel}); err != nil {
			return err
		}
		if len(nodes.Items) == 0 {
			return fmt.Errorf("no nodes carrying the %s label found", karpenterNodePoolLabel)
		}
		for _, node := range nodes.Items {
			logger.Log("Karpenter node %s in pool %q (capacity-type %q, instance-type %q)",
				node.Name,
				node.Labels[karpenterNodePoolLabel],
				node.Labels["karpenter.sh/capacity-type"],
				node.Labels["node.kubernetes.io/instance-type"],
			)
		}
		return nil
	}).
		WithTimeout(20 * time.Minute).
		WithPolling(30 * time.Second).
		Should(Succeed())
}

// gatewayKarpenterProxyPodTests validates the Karpenter-related scheduling and
// shutdown settings the chart puts on the envoy proxy pods of an AWS NLB gateway.
// These let Karpenter disrupt gateway nodes while giving envoy enough time to
// drain when a node goes away.
func gatewayKarpenterProxyPodTests() {
	wcClient, _ := state.GetFramework().WC(state.GetCluster().Name)

	By("checking envoy proxy pods do not opt out of Karpenter disruption")
	Eventually(func() error {
		proxyPods, err := gatewayProxyPods(wcClient)
		if err != nil {
			return err
		}
		for _, pod := range proxyPods.Items {
			if got, ok := pod.Annotations[doNotDisruptAnnotation]; ok {
				return fmt.Errorf("pod %s/%s has annotation %s=%q, expected it to be absent", pod.Namespace, pod.Name, doNotDisruptAnnotation, got)
			}
		}
		logger.Log("None of the (%d) envoy proxy pods carry %s", len(proxyPods.Items), doNotDisruptAnnotation)
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	By("checking envoy proxy pods have a terminationGracePeriodSeconds above the drain timeout")
	Eventually(func() error {
		proxyPods, err := gatewayProxyPods(wcClient)
		if err != nil {
			return err
		}
		for _, pod := range proxyPods.Items {
			if pod.Spec.TerminationGracePeriodSeconds == nil {
				return fmt.Errorf("pod %s/%s has no terminationGracePeriodSeconds set", pod.Namespace, pod.Name)
			}
			if got := *pod.Spec.TerminationGracePeriodSeconds; got != expectedTerminationGracePeriod {
				return fmt.Errorf("pod %s/%s has terminationGracePeriodSeconds %d, expected %d", pod.Namespace, pod.Name, got, expectedTerminationGracePeriod)
			}
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	By("checking envoy proxy pods prefer one pod per node")
	Eventually(func() error {
		proxyPods, err := gatewayProxyPods(wcClient)
		if err != nil {
			return err
		}
		for _, pod := range proxyPods.Items {
			if pod.Spec.Affinity == nil || pod.Spec.Affinity.PodAntiAffinity == nil {
				return fmt.Errorf("pod %s/%s has no podAntiAffinity", pod.Namespace, pod.Name)
			}
			found := false
			for _, term := range pod.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
				if term.PodAffinityTerm.TopologyKey == hostnameTopologyKey {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("pod %s/%s has no preferred podAntiAffinity term on topologyKey %s", pod.Namespace, pod.Name, hostnameTopologyKey)
			}
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	By("checking envoy proxy pods are spread across nodes")
	proxyPods, err := gatewayProxyPods(wcClient)
	Expect(err).NotTo(HaveOccurred())

	workers, err := readyWorkerNodes(wcClient)
	Expect(err).NotTo(HaveOccurred())

	nodesByName := map[string]int{}
	for _, pod := range proxyPods.Items {
		nodesByName[pod.Spec.NodeName]++
	}
	logger.Log("%d envoy proxy pods spread over %d of %d worker nodes", len(proxyPods.Items), len(nodesByName), len(workers))

	// Reported, not asserted. The chart asks for one proxy per node with a
	// preferred anti-affinity term, which the spec assertion above pins, but the
	// scheduler is free to stack the pods: it does so whenever the nodes it would
	// spread over are not schedulable yet, which is the normal case here since
	// Karpenter provisions its node after the proxies are already placed, and
	// nothing moves them afterwards. Failing on the outcome would make this spec
	// depend on node timing rather than on anything the chart controls.
	if len(nodesByName) < len(proxyPods.Items) {
		entry := fmt.Sprintf("%d envoy proxy pods on %d nodes (%v), with %d ready workers",
			len(proxyPods.Items), len(nodesByName), nodesByName, len(workers))
		logger.Log("Envoy proxy pods are not spread one per node: %s", entry)
		AddReportEntry("envoy proxy pod spread", entry)
	}
}

// gatewayProxyPods returns the envoy proxy pods of the giantswarm-default gateway.
// It errors when none are found so it can be used inside an Eventually block.
func gatewayProxyPods(wcClient *client.Client) (*corev1.PodList, error) {
	proxyPods := &corev1.PodList{}
	err := wcClient.List(state.GetContext(), proxyPods, &cr.ListOptions{
		Namespace: "envoy-gateway-system",
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"gateway.envoyproxy.io/owning-gateway-name":      "giantswarm-default",
			"gateway.envoyproxy.io/owning-gateway-namespace": "envoy-gateway-system",
		}),
	})
	if err != nil {
		return nil, err
	}
	if len(proxyPods.Items) == 0 {
		return nil, fmt.Errorf("no envoy proxy pods found for gateway giantswarm-default")
	}
	return proxyPods, nil
}

// readyWorkerNodes returns the Ready nodes that are not control plane nodes.
func readyWorkerNodes(wcClient *client.Client) ([]corev1.Node, error) {
	nodes := &corev1.NodeList{}
	if err := wcClient.List(state.GetContext(), nodes, client.DoesNotHaveLabels{"node-role.kubernetes.io/control-plane"}); err != nil {
		return nil, err
	}

	ready := []corev1.Node{}
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = append(ready, node)
				break
			}
		}
	}
	return ready, nil
}
