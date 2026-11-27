package basic

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	cr "sigs.k8s.io/controller-runtime/pkg/client"

	"e2e/internal/common"
)

const (
	// readinessListenerPort and readinessListenerPath are where the chart's readiness
	// listener answers on the proxy pods of an AWS NLB gateway.
	readinessListenerPort = 19004
	readinessListenerPath = "/ready"
	// envoyGatewayReadinessPort is Envoy Gateway's own readiness listener, which the
	// startup and liveness probes keep using.
	envoyGatewayReadinessPort       = 19003
	expectedHealthCheckFailureDelay = "--health-check-failure-delay=1s"

	// rolloutRequestInterval sets the rate of new connections sent through the NLB
	// while the proxies roll: one request every 50ms, 20 per second.
	rolloutRequestInterval = 50 * time.Millisecond
	// rolloutSettleTime keeps the traffic going after the last old proxy pod is gone,
	// while checking that the new proxy pods stay running.
	rolloutSettleTime = 30 * time.Second
	// rolloutMaxFailureRatio tolerates a stray network error between the test runner
	// and the NLB. Without the readiness listener a rollout of two proxies fails
	// around 5% of the new connections.
	rolloutMaxFailureRatio = 0.005
)

// gatewayReadinessListenerTests validates that the envoy readiness probe of the
// default gateway's proxy pods points at the chart's readiness listener, that the
// startup probe still uses Envoy Gateway's /ready, and that the shutdown-manager
// preStop hook carries the 1s health check failure delay.
func gatewayReadinessListenerTests() {
	wcClient := common.WC()

	By("checking the envoy readiness probe uses the readiness listener")
	Eventually(func(g Gomega) {
		pods, err := common.GatewayProxyPods(wcClient)
		g.Expect(err).NotTo(HaveOccurred())
		for _, pod := range pods.Items {
			envoy := podContainer(pod, "envoy")
			g.Expect(envoy).NotTo(BeNil(), "pod %s/%s has no envoy container", pod.Namespace, pod.Name)
			g.Expect(envoy.ReadinessProbe).NotTo(BeNil())
			g.Expect(envoy.ReadinessProbe.HTTPGet).NotTo(BeNil())
			g.Expect(envoy.ReadinessProbe.HTTPGet.Port.IntValue()).To(Equal(readinessListenerPort),
				"pod %s/%s readiness probe port", pod.Namespace, pod.Name)
			g.Expect(envoy.ReadinessProbe.HTTPGet.Path).To(Equal(readinessListenerPath),
				"pod %s/%s readiness probe path", pod.Namespace, pod.Name)
			g.Expect(envoy.StartupProbe).NotTo(BeNil())
			g.Expect(envoy.StartupProbe.HTTPGet).NotTo(BeNil())
			g.Expect(envoy.StartupProbe.HTTPGet.Port.IntValue()).To(Equal(envoyGatewayReadinessPort),
				"pod %s/%s startup probe port", pod.Namespace, pod.Name)

			shutdownManager := podContainer(pod, "shutdown-manager")
			g.Expect(shutdownManager).NotTo(BeNil(), "pod %s/%s has no shutdown-manager container", pod.Namespace, pod.Name)
			g.Expect(shutdownManager.Lifecycle).NotTo(BeNil())
			g.Expect(shutdownManager.Lifecycle.PreStop).NotTo(BeNil())
			g.Expect(shutdownManager.Lifecycle.PreStop.Exec).NotTo(BeNil())
			g.Expect(shutdownManager.Lifecycle.PreStop.Exec.Command).To(ContainElement(expectedHealthCheckFailureDelay),
				"pod %s/%s shutdown-manager preStop", pod.Namespace, pod.Name)
		}
		logger.Log("All (%d) envoy proxy pods probe readiness on :%d%s", len(pods.Items), readinessListenerPort, readinessListenerPath)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// gatewayRolloutBehaviorTest restarts the default gateway's proxy Deployment while
// sending new connections to the NLB, and checks that they keep being answered until
// every old proxy pod is gone. Without the readiness listener a terminating pod stops
// serving while the NLB still routes to its node, and the node refuses the connections
// until the NLB marks it unhealthy.
func gatewayRolloutBehaviorTest() {
	wcClient := common.WC()
	ctx := state.GetContext()

	var hostname string
	Eventually(func() error {
		var err error
		hostname, err = getGatewayLBHostname(wcClient)
		if err != nil {
			return fmt.Errorf("getting the gateway load balancer hostname: %w", err)
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
	url := fmt.Sprintf("http://%s/", hostname)

	httpClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	By("checking the gateway answers before the rollout")
	Eventually(func() error {
		return probeGateway(httpClient, url)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	var deployment appsv1.Deployment
	Eventually(func() error {
		deployments := &appsv1.DeploymentList{}
		if err := wcClient.List(ctx, deployments, &cr.ListOptions{
			Namespace:     common.ProxyNamespace,
			LabelSelector: common.DefaultGatewayProxySelector,
		}); err != nil {
			return fmt.Errorf("listing the proxy Deployments: %w", err)
		}
		if len(deployments.Items) != 1 {
			return fmt.Errorf("expected one proxy Deployment for gateway %s, found %d", common.DefaultGatewayName, len(deployments.Items))
		}
		deployment = deployments.Items[0]
		return nil
	}).
		WithTimeout(time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	oldPods, err := common.GatewayProxyPods(wcClient)
	Expect(err).NotTo(HaveOccurred())

	By("sending new connections to the NLB")
	var total, failed atomic.Int64
	var failuresMu sync.Mutex
	var failures []string
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(rolloutRequestInterval)
		defer ticker.Stop()
		var requests sync.WaitGroup
		defer requests.Wait()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				requests.Go(func() {
					total.Add(1)
					if err := probeGateway(httpClient, url); err != nil {
						failed.Add(1)
						failuresMu.Lock()
						failures = append(failures, fmt.Sprintf("%s %v", time.Now().UTC().Format(time.RFC3339), err))
						failuresMu.Unlock()
					}
				})
			}
		}
	})
	defer func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		wg.Wait()
	}()

	By("restarting the proxy Deployment")
	patch := cr.MergeFrom(deployment.DeepCopy())
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	Expect(wcClient.Patch(ctx, &deployment, patch)).To(Succeed())

	By("waiting for every old proxy pod to be gone")
	Eventually(func() error {
		for _, pod := range oldPods.Items {
			err := wcClient.Get(ctx, cr.ObjectKeyFromObject(&pod), &corev1.Pod{})
			if err == nil {
				return fmt.Errorf("old proxy pod %s/%s still exists", pod.Namespace, pod.Name)
			}
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting old proxy pod %s/%s: %w", pod.Namespace, pod.Name, err)
			}
		}
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	By("checking the new proxy pods stay running while the traffic settles")
	proxyPodsListOptions := &cr.ListOptions{
		Namespace:     common.ProxyNamespace,
		LabelSelector: common.DefaultGatewayProxySelector,
	}
	Consistently(arePodsRunning(ctx, wcClient, proxyPodsListOptions)).
		WithTimeout(rolloutSettleTime).
		WithPolling(5 * time.Second).
		Should(BeTrue())
	close(stop)
	wg.Wait()

	sent, lost := total.Load(), failed.Load()
	logger.Log("Proxy rollout: %d of %d new connections failed", lost, sent)
	for _, f := range failures {
		logger.Log("failed request: %s", f)
	}
	if lost > 0 {
		AddReportEntry("proxy rollout failed requests", fmt.Sprintf("%d of %d", lost, sent))
	}
	Expect(sent).To(BeNumerically(">", 0))
	Expect(float64(lost)/float64(sent)).To(BeNumerically("<=", rolloutMaxFailureRatio),
		"%d of %d new connections failed during the proxy rollout:\n%s", lost, sent, strings.Join(failures, "\n"))

}

// probeGateway sends one request over a new connection and expects the HTTP to HTTPS
// redirect of the http listener. Any HTTP answer means envoy accepted the connection;
// a refused or reset connection is the failure this test looks for.
func probeGateway(httpClient *http.Client, url string) error {
	resp, err := httpClient.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusMovedPermanently {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}
	return nil
}

func podContainer(pod corev1.Pod, name string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}
