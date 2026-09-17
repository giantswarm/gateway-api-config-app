package basic

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// clientIPProbeName names every resource the probe needs. They all live in the
	// gateway namespace because the http listener only accepts routes from Same.
	clientIPProbeName      = "xff-probe"
	clientIPProbeNamespace = "envoy-gateway-system"
	clientIPProbePort      = 8080
	// busybox httpd hands request headers to CGI scripts as HTTP_* environment
	// variables, which is all the probe needs to report what actually reached it.
	clientIPProbeImage = "gsoci.azurecr.io/giantswarm/busybox:1.38.0"
	// forgedClientIP is what an attacker sends to make a backend attribute the request
	// to someone else. In the incident behind this test, brute force attempts carrying
	// this header had Grafana block 127.0.0.1, and with it the observability-operator.
	forgedClientIP = "127.0.0.1"
)

// clientIPProbeScript serves a CGI script that reports the client identity headers the
// backend received. It writes under /tmp so the container can run as a non-root user.
const clientIPProbeScript = `set -e
mkdir -p /tmp/www/cgi-bin
cat > /tmp/www/cgi-bin/echo <<'EOS'
#!/bin/sh
echo "Content-Type: text/plain"
echo
echo "x-forwarded-for=${HTTP_X_FORWARDED_FOR}"
echo "x-real-ip=${HTTP_X_REAL_IP}"
EOS
chmod +x /tmp/www/cgi-bin/echo
exec httpd -f -v -p 8080 -h /tmp/www
`

// gatewayClientIPBehaviorTest sends a request carrying a forged X-Forwarded-For through the
// gateway's LoadBalancer and asserts the backend never sees it. Asserting the rendered
// ClientTrafficPolicy is not enough on its own: clientIPDetection settings change what envoy
// itself trusts while still forwarding the client's header, so only a request that reaches a
// backend proves the header was rebuilt rather than appended to.
func gatewayClientIPBehaviorTest() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("deploying a probe backend that reports the client identity headers it receives")
	deployment := clientIPProbeDeployment()
	service := clientIPProbeService()
	route := clientIPProbeRoute()

	for _, obj := range []cr.Object{deployment, service, route} {
		err := wcClient.Create(state.GetContext(), obj)
		if errors.IsAlreadyExists(err) {
			// Left behind by an interrupted run: the probe is stateless, so reuse it.
			logger.Log("Probe resource %s already exists, reusing it", obj.GetName())
			continue
		}
		Expect(err).NotTo(HaveOccurred())
	}

	DeferCleanup(func() {
		for _, obj := range []cr.Object{route, service, deployment} {
			if err := wcClient.Delete(state.GetContext(), obj); err != nil && !errors.IsNotFound(err) {
				logger.Log("Failed to delete probe resource %s: %v", obj.GetName(), err)
			}
		}
	})

	By("waiting for the probe backend to become ready")
	Eventually(func() (int32, error) {
		probe := &appsv1.Deployment{}
		if err := wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      clientIPProbeName,
			Namespace: clientIPProbeNamespace,
		}, probe); err != nil {
			return 0, err
		}
		return probe.Status.ReadyReplicas, nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeNumerically(">=", 1))

	By("sending a request with a forged X-Forwarded-For through the gateway LoadBalancer")
	httpClient := &http.Client{Timeout: 10 * time.Second}
	observed := map[string]string{}
	Eventually(func() error {
		hostname, err := getGatewayLBHostname(wcClient)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(state.GetContext(), http.MethodGet,
			fmt.Sprintf("http://%s/%s", hostname, clientIPProbeName), nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Forwarded-For", forgedClientIP)
		req.Header.Set("X-Real-IP", forgedClientIP)

		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close() //nolint:errcheck

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("expected 200 from the probe, got %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		observed = parseProbeResponse(string(body))
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(15 * time.Second).
		Should(Succeed())

	logger.Log("Probe backend received x-forwarded-for=%q x-real-ip=%q",
		observed["x-forwarded-for"], observed["x-real-ip"])

	By("checking envoy rebuilt X-Forwarded-For from the connection source address")
	addresses := strings.Split(observed["x-forwarded-for"], ",")
	Expect(addresses).To(HaveLen(1),
		"expected a single address in X-Forwarded-For, got %q", observed["x-forwarded-for"])

	clientIP := strings.TrimSpace(addresses[0])
	Expect(clientIP).NotTo(Equal(forgedClientIP), "the forged X-Forwarded-For reached the backend")
	Expect(net.ParseIP(clientIP)).NotTo(BeNil(),
		"expected X-Forwarded-For to hold an IP address, got %q", clientIP)

	By("checking the forged X-Real-IP was dropped")
	Expect(observed["x-real-ip"]).To(BeEmpty(), "the forged X-Real-IP reached the backend")
}

// parseProbeResponse turns the probe's "key=value" lines into a map.
func parseProbeResponse(body string) map[string]string {
	parsed := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		parsed[key] = strings.TrimSpace(value)
	}
	return parsed
}

func clientIPProbeLabels() map[string]string {
	return map[string]string{"app": clientIPProbeName}
}

func clientIPProbeDeployment() *appsv1.Deployment {
	replicas := int32(1)
	user := int64(65534)
	runAsNonRoot := true
	allowPrivilegeEscalation := false

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientIPProbeName,
			Namespace: clientIPProbeNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: clientIPProbeLabels()},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: clientIPProbeLabels()},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &runAsNonRoot,
						RunAsUser:      &user,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:    clientIPProbeName,
						Image:   clientIPProbeImage,
						Command: []string{"sh", "-c", clientIPProbeScript},
						Ports: []corev1.ContainerPort{{
							ContainerPort: clientIPProbePort,
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &allowPrivilegeEscalation,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
}

func clientIPProbeService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientIPProbeName,
			Namespace: clientIPProbeNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: clientIPProbeLabels(),
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt32(clientIPProbePort),
			}},
		},
	}
}

// clientIPProbeRoute exposes the probe on the http listener. The path match is longer than
// the "/" of the chart's TLS redirect route, so it takes precedence over the redirect and the
// request reaches the backend instead of being answered with a 301.
func clientIPProbeRoute() *unstructured.Unstructured {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "HTTPRoute",
	})
	route.SetName(clientIPProbeName)
	route.SetNamespace(clientIPProbeNamespace)
	route.Object["spec"] = map[string]any{
		"parentRefs": []any{map[string]any{
			"name":        "giantswarm-default",
			"sectionName": "http",
		}},
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{
				"path": map[string]any{
					"type":  "PathPrefix",
					"value": "/" + clientIPProbeName,
				},
			}},
			"filters": []any{map[string]any{
				"type": "URLRewrite",
				"urlRewrite": map[string]any{
					"path": map[string]any{
						"type":               "ReplacePrefixMatch",
						"replacePrefixMatch": "/cgi-bin/echo",
					},
				},
			}},
			"backendRefs": []any{map[string]any{
				"name": clientIPProbeName,
				"port": int64(80),
			}},
		}},
	}
	return route
}
