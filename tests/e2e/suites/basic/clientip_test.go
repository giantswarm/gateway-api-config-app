package basic

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/client"
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
env | grep '^HTTP_'
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

	// Registered last so it runs first: whether the strip reached the proxies at all is
	// only answerable from the live listener config, and the probe teardown above would
	// not affect it, but the ordering keeps the dump next to the failure in the output.
	DeferCleanup(func() {
		if CurrentSpecReport().Failed() {
			dumpClientIPDiagnostics(wcClient)
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
	rawBody := ""
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
		rawBody = string(body)
		observed = parseProbeResponse(rawBody)
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(15 * time.Second).
		Should(Succeed())

	logger.Log("Probe backend received:\n%s", strings.TrimSpace(rawBody))

	xff := observed["x-forwarded-for"]
	addresses := []string{}
	for _, address := range strings.Split(xff, ",") {
		addresses = append(addresses, strings.TrimSpace(address))
	}

	// Assert the forged value first. It is the property the chart actually guarantees, and
	// checking the count first reports "expected a single address" for what is really
	// "the strip did not happen", which sends the next reader down the wrong path.
	By("checking the forged client identity headers did not reach the backend")
	Expect(addresses).NotTo(ContainElement(forgedClientIP),
		"the forged X-Forwarded-For reached the backend, so the header was appended to rather than rebuilt: %q", xff)
	Expect(observed["x-real-ip"]).To(BeEmpty(), "the forged X-Real-IP reached the backend")

	// Everything the client sent is dropped, including any hop a forward proxy in front of
	// the test added, so envoy's own append is the only address left.
	By("checking envoy rebuilt X-Forwarded-For from the connection source address")
	Expect(addresses).To(HaveLen(1),
		"expected a single address in X-Forwarded-For, got %q", xff)
	Expect(net.ParseIP(addresses[0])).NotTo(BeNil(),
		"expected X-Forwarded-For to hold an IP address, got %q", addresses[0])
}

// dumpClientIPDiagnostics logs what determines whether the header strip was programmed onto
// the proxies at all: the policy as stored and as reconciled, and whether the live listener
// config carries the early header mutation the chart asks for. A policy can report Accepted
// while its settings never reach the listener, so the CR alone does not answer this.
func dumpClientIPDiagnostics(wcClient *client.Client) {
	ctp := &unstructured.Unstructured{}
	ctp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    "ClientTrafficPolicy",
	})
	if err := wcClient.Get(state.GetContext(), cr.ObjectKey{
		Name:      "gateway-giantswarm-default",
		Namespace: clientIPProbeNamespace,
	}, ctp); err != nil {
		logger.Log("Diagnostics: could not read the ClientTrafficPolicy: %v", err)
	} else {
		spec, _ := json.Marshal(ctp.Object["spec"])
		status, _ := json.Marshal(ctp.Object["status"])
		logger.Log("Diagnostics: ClientTrafficPolicy spec: %s", spec)
		logger.Log("Diagnostics: ClientTrafficPolicy status: %s", status)
	}

	proxyPods, err := gatewayProxyPods(wcClient)
	if err != nil {
		logger.Log("Diagnostics: could not list the envoy proxy pods: %v", err)
		return
	}

	pod := proxyPods.Items[0]
	container := ""
	for _, c := range pod.Spec.Containers {
		if strings.HasPrefix(c.Image, "gsoci.azurecr.io/giantswarm/envoy") {
			container = c.Name
			break
		}
	}
	if container == "" {
		logger.Log("Diagnostics: no envoy container found in pod %s/%s", pod.Namespace, pod.Name)
		return
	}

	// The admin interface listens on localhost only, so the dump has to be fetched from
	// inside the container. Fall back to wget in case the image carries no curl.
	dumpURL := "http://localhost:19000/config_dump?resource=dynamic_listeners"
	stdout, stderr, err := wcClient.ExecInPod(state.GetContext(), pod.Name, pod.Namespace, container,
		[]string{"sh", "-c", fmt.Sprintf("curl -s '%s' || wget -q -O - '%s'", dumpURL, dumpURL)})
	if err != nil {
		logger.Log("Diagnostics: could not dump the envoy config from %s/%s: %v (stderr: %s)",
			pod.Namespace, pod.Name, err, strings.TrimSpace(stderr))
		return
	}

	logger.Log("Diagnostics: listener config from %s/%s is %d bytes, earlyHeaderMutation present: %t, useRemoteAddress present: %t",
		pod.Namespace, pod.Name, len(stdout),
		strings.Contains(stdout, "early_header_mutation") || strings.Contains(stdout, "earlyHeaderMutation"),
		strings.Contains(stdout, "use_remote_address") || strings.Contains(stdout, "useRemoteAddress"))

	for _, marker := range []string{"earlyHeaderMutation", "early_header_mutation", "useRemoteAddress", "use_remote_address"} {
		if excerpt := configExcerpt(stdout, marker, 600); excerpt != "" {
			logger.Log("Diagnostics: listener config around %q:\n%s", marker, excerpt)
			break
		}
	}
}

// configExcerpt returns a window of the config dump around the first occurrence of marker,
// so the log carries the relevant part of a dump that runs to megabytes.
func configExcerpt(dump, marker string, window int) string {
	index := strings.Index(dump, marker)
	if index < 0 {
		return ""
	}
	start := max(index-window, 0)
	end := min(index+window, len(dump))
	return dump[start:end]
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
