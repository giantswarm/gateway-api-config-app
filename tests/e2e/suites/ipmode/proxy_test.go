package ipmode

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/client"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"
	cr "sigs.k8s.io/controller-runtime/pkg/client"

	"e2e/internal/common"
)

const (
	// expectedReplicas is pinned by envoyHpa in bundle_values.yaml, so the HPA cannot
	// change the pod count while the suite replaces and counts pods.
	expectedReplicas = 3
)

// proxyPods returns the gateway's proxy pods that are not being deleted.
func proxyPods(c *client.Client) ([]corev1.Pod, error) {
	list := &corev1.PodList{}
	if err := c.List(state.GetContext(), list, &cr.ListOptions{Namespace: common.ProxyNamespace, LabelSelector: common.DefaultGatewayProxySelector}); err != nil {
		return nil, err
	}
	pods := []corev1.Pod{}
	for _, pod := range list.Items {
		if pod.DeletionTimestamp == nil {
			pods = append(pods, pod)
		}
	}
	return pods, nil
}

func isPodReady(pod corev1.Pod) bool {
	return podConditionTrue(pod, corev1.PodReady)
}

func podConditionTrue(pod corev1.Pod, conditionType corev1.PodConditionType) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == conditionType {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// waitForReadyProxyPods waits until exactly expectedReplicas proxy pods exist and
// all of them are Ready, and returns them.
func waitForReadyProxyPods(c *client.Client, timeout time.Duration) []corev1.Pod {
	var ready []corev1.Pod
	Eventually(func() error {
		pods, err := proxyPods(c)
		if err != nil {
			return err
		}
		if len(pods) != expectedReplicas {
			return fmt.Errorf("found %d proxy pods, want %d", len(pods), expectedReplicas)
		}
		for _, pod := range pods {
			if !isPodReady(pod) {
				return fmt.Errorf("proxy pod %s is not Ready", pod.Name)
			}
		}
		ready = pods
		return nil
	}).
		WithTimeout(timeout).
		WithPolling(10 * time.Second).
		Should(Succeed())
	return ready
}

// proxyRunningTests waits for the gateway's proxy pods to run at the pinned replica
// count before the suite starts changing them.
func proxyRunningTests() {
	By("checking the proxy pods of the gateway are Ready")
	pods := waitForReadyProxyPods(common.WC(), 15*time.Minute)
	for _, pod := range pods {
		logger.Log("Proxy pod %s on node %s", pod.Name, pod.Spec.NodeName)
	}
}
