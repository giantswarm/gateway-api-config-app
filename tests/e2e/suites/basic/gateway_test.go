package basic

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v4/pkg/state"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

// gatewayGatewayTests verifies that the Gateway resource exists, has correct listeners on ports 80/443,
// and reaches Accepted and Programmed states, confirming the gateway is configured and ready to handle traffic.
func gatewayGatewayTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking Gateway giantswarm-default exists in envoy-gateway-system")
	gateway := &unstructured.Unstructured{}
	gateway.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "Gateway",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "giantswarm-default",
			Namespace: "envoy-gateway-system",
		}, gateway)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking Gateway spec.gatewayClassName = giantswarm-default")
	spec := gateway.Object["spec"].(map[string]any)
	Expect(spec["gatewayClassName"]).To(Equal("giantswarm-default"))

	By("checking Gateway has two listeners: http (port 80) and https (port 443)")
	listeners := spec["listeners"].([]any)
	Expect(listeners).To(HaveLen(2))
	listenersByName := map[string]map[string]any{}
	for _, l := range listeners {
		listener := l.(map[string]any)
		listenersByName[listener["name"].(string)] = listener
	}
	Expect(listenersByName).To(HaveKey("http"))
	Expect(listenersByName["http"]["port"]).To(BeEquivalentTo(80))
	Expect(listenersByName["http"]["protocol"]).To(Equal("HTTP"))
	Expect(listenersByName).To(HaveKey("https"))
	Expect(listenersByName["https"]["port"]).To(BeEquivalentTo(443))
	Expect(listenersByName["https"]["protocol"]).To(Equal("HTTPS"))

	By("checking Gateway is Accepted and Programmed")
	Eventually(func() (bool, error) {
		if err := wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "giantswarm-default",
			Namespace: "envoy-gateway-system",
		}, gateway); err != nil {
			return false, err
		}
		status, ok := gateway.Object["status"].(map[string]any)
		if !ok {
			return false, nil
		}
		conditions, ok := status["conditions"].([]any)
		if !ok {
			return false, nil
		}
		accepted, programmed := false, false
		for _, c := range conditions {
			condition := c.(map[string]any)
			switch condition["type"] {
			case "Accepted":
				accepted = condition["status"] == "True"
			case "Programmed":
				programmed = condition["status"] == "True"
			}
		}
		return accepted && programmed, nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeTrue())
}

// gatewayEnvoyProxyTests confirms EnvoyProxy is configured with Giant Swarm's image registry,
// correct HPA autoscaling bounds (2-10 replicas), and PDB disruption budget to ensure availability during updates.
func gatewayEnvoyProxyTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking EnvoyProxy gateway-giantswarm-default exists in envoy-gateway-system")
	envoyProxy := &unstructured.Unstructured{}
	envoyProxy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    "EnvoyProxy",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default",
			Namespace: "envoy-gateway-system",
		}, envoyProxy)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking EnvoyProxy imageRepository starts with gsoci.azurecr.io/giantswarm/envoy")
	envoySpec := envoyProxy.Object["spec"].(map[string]any)
	provider := envoySpec["provider"].(map[string]any)
	kubernetes := provider["kubernetes"].(map[string]any)
	envoyDeployment := kubernetes["envoyDeployment"].(map[string]any)
	container := envoyDeployment["container"].(map[string]any)
	imageRepo := container["imageRepository"].(string)
	Expect(strings.HasPrefix(imageRepo, "gsoci.azurecr.io/giantswarm/envoy")).To(BeTrue(),
		"expected imageRepository to start with gsoci.azurecr.io/giantswarm/envoy, got: %s", imageRepo)

	By("checking EnvoyProxy HPA minReplicas=2, maxReplicas=10")
	hpa := kubernetes["envoyHpa"].(map[string]any)
	Expect(hpa["minReplicas"]).To(BeEquivalentTo(2))
	Expect(hpa["maxReplicas"]).To(BeEquivalentTo(10))

	By("checking EnvoyProxy PDB maxUnavailable=25%")
	pdb := kubernetes["envoyPDB"].(map[string]any)
	Expect(pdb["maxUnavailable"]).To(Equal("25%"))
}

// gatewayClientTrafficPolicyTests validates ClientTrafficPolicy correctly targets the gateway,
// enforces PROXY protocol handling, and defines health check path for AWS NLB to detect healthy proxies.
func gatewayClientTrafficPolicyTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking ClientTrafficPolicy gateway-giantswarm-default exists in envoy-gateway-system")
	ctp := &unstructured.Unstructured{}
	ctp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    "ClientTrafficPolicy",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default",
			Namespace: "envoy-gateway-system",
		}, ctp)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking ClientTrafficPolicy targetRefs name=giantswarm-default, kind=Gateway")
	ctpSpec := ctp.Object["spec"].(map[string]any)
	targetRefs := ctpSpec["targetRefs"].([]any)
	Expect(targetRefs).To(HaveLen(1))
	targetRef := targetRefs[0].(map[string]any)
	Expect(targetRef["name"]).To(Equal("giantswarm-default"))
	Expect(targetRef["kind"]).To(Equal("Gateway"))

	By("checking ClientTrafficPolicy proxyProtocol.optional=false")
	proxyProtocol := ctpSpec["proxyProtocol"].(map[string]any)
	Expect(proxyProtocol["optional"]).To(BeFalse())

	By("checking ClientTrafficPolicy healthCheck.path=/healthz")
	healthCheck := ctpSpec["healthCheck"].(map[string]any)
	Expect(healthCheck["path"]).To(Equal("/healthz"))
}

// gatewayBackendTrafficPolicyTests validates BackendTrafficPolicy is configured to return
// custom error pages for 5xx status codes, targeting the default gateway.
func gatewayBackendTrafficPolicyTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking BackendTrafficPolicy gateway-giantswarm-default-error-pages exists in envoy-gateway-system")
	btp := &unstructured.Unstructured{}
	btp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    "BackendTrafficPolicy",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default-error-pages",
			Namespace: "envoy-gateway-system",
		}, btp)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking BackendTrafficPolicy targetRefs name=giantswarm-default, kind=Gateway")
	btpSpec := btp.Object["spec"].(map[string]any)
	targetRefs := btpSpec["targetRefs"].([]any)
	Expect(targetRefs).To(HaveLen(1))
	targetRef := targetRefs[0].(map[string]any)
	Expect(targetRef["name"]).To(Equal("giantswarm-default"))
	Expect(targetRef["kind"]).To(Equal("Gateway"))

	By("checking BackendTrafficPolicy responseOverride has Value and Range status codes")
	responseOverride := btpSpec["responseOverride"].([]any)
	Expect(responseOverride).To(HaveLen(1))
	override := responseOverride[0].(map[string]any)
	match := override["match"].(map[string]any)
	statusCodes := match["statusCodes"].([]any)
	Expect(statusCodes).To(HaveLen(3))

	// First two are Value entries (500, 502)
	code0 := statusCodes[0].(map[string]any)
	Expect(code0["type"]).To(Equal("Value"))
	Expect(code0["value"]).To(BeEquivalentTo(500))

	code1 := statusCodes[1].(map[string]any)
	Expect(code1["type"]).To(Equal("Value"))
	Expect(code1["value"]).To(BeEquivalentTo(502))

	// Third is a Range entry (503-504)
	code2 := statusCodes[2].(map[string]any)
	Expect(code2["type"]).To(Equal("Range"))
	code2Range := code2["range"].(map[string]any)
	Expect(code2Range["start"]).To(BeEquivalentTo(503))
	Expect(code2Range["end"]).To(BeEquivalentTo(504))

	By("checking BackendTrafficPolicy response contentType=text/html and body references ConfigMap")
	response := override["response"].(map[string]any)
	Expect(response["contentType"]).To(Equal("text/html"))
	body := response["body"].(map[string]any)
	Expect(body["type"]).To(Equal("ValueRef"))
	valueRef := body["valueRef"].(map[string]any)
	Expect(valueRef["kind"]).To(Equal("ConfigMap"))
	Expect(valueRef["name"]).To(Equal("gateway-giantswarm-default-error-pages"))

	By("checking BackendTrafficPolicy is Accepted")
	Eventually(func() (bool, error) {
		if err := wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default-error-pages",
			Namespace: "envoy-gateway-system",
		}, btp); err != nil {
			return false, err
		}
		status, ok := btp.Object["status"].(map[string]any)
		if !ok {
			return false, nil
		}
		ancestors, ok := status["ancestors"].([]any)
		if !ok {
			return false, nil
		}
		for _, a := range ancestors {
			ancestor := a.(map[string]any)
			ancestorRef, ok := ancestor["ancestorRef"].(map[string]any)
			if !ok {
				continue
			}
			if ancestorRef["kind"] != "Gateway" || ancestorRef["name"] != "giantswarm-default" {
				continue
			}
			conditions, ok := ancestor["conditions"].([]any)
			if !ok {
				continue
			}
			for _, c := range conditions {
				condition := c.(map[string]any)
				if condition["type"] == "Accepted" {
					return condition["status"] == "True", nil
				}
			}
		}
		return false, nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeTrue())
}

// gatewayIssuerTests verifies the cert-manager Issuer exists with Let's Encrypt configuration
// and reaches Ready state, ensuring TLS certificates can be provisioned for HTTPS.
func gatewayIssuerTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking Issuer letsencrypt-giantswarm-gateway exists in envoy-gateway-system")
	issuer := &unstructured.Unstructured{}
	issuer.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Issuer",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "letsencrypt-giantswarm-gateway",
			Namespace: "envoy-gateway-system",
		}, issuer)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking Issuer spec.acme.email = accounts@giantswarm.io")
	issuerSpec := issuer.Object["spec"].(map[string]any)
	acme := issuerSpec["acme"].(map[string]any)
	Expect(acme["email"]).To(Equal("accounts@giantswarm.io"))

	By("checking Issuer spec.acme.server contains letsencrypt.org")
	server := acme["server"].(string)
	Expect(server).To(ContainSubstring("letsencrypt.org"))

	By("waiting for Issuer to reach Ready=True")
	Eventually(func() (bool, error) {
		if err := wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "letsencrypt-giantswarm-gateway",
			Namespace: "envoy-gateway-system",
		}, issuer); err != nil {
			return false, err
		}
		status, ok := issuer.Object["status"].(map[string]any)
		if !ok {
			return false, nil
		}
		conditions, ok := status["conditions"].([]any)
		if !ok {
			return false, nil
		}
		for _, c := range conditions {
			condition := c.(map[string]any)
			if condition["type"] == "Ready" {
				return condition["status"] == "True", nil
			}
		}
		return false, nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(BeTrue())
}

// gatewayCertificateTests confirms the TLS certificate is created with proper issuer references
// and reaches Ready state, ensuring HTTPS is fully functional on the gateway.
func gatewayCertificateTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking Certificate gateway-giantswarm-default-https exists in envoy-gateway-system")
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default-https",
			Namespace: "envoy-gateway-system",
		}, cert)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking Certificate issuerRef.kind = ClusterIssuer")
	certSpec := cert.Object["spec"].(map[string]any)
	issuerRef := certSpec["issuerRef"].(map[string]any)
	Expect(issuerRef["kind"]).To(Equal("ClusterIssuer"))

	By("checking Certificate issuerRef.name = letsencrypt-giantswarm")
	Expect(issuerRef["name"]).To(Equal("letsencrypt-giantswarm"))

	By("checking Certificate secretName = gateway-giantswarm-default-https-tls")
	Expect(certSpec["secretName"]).To(Equal("gateway-giantswarm-default-https-tls"))

	By("waiting for Certificate to reach Ready=True")
	Eventually(func() (bool, error) {
		if err := wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default-https",
			Namespace: "envoy-gateway-system",
		}, cert); err != nil {
			return false, err
		}
		status, ok := cert.Object["status"].(map[string]any)
		if !ok {
			return false, nil
		}
		conditions, ok := status["conditions"].([]any)
		if !ok {
			return false, nil
		}
		for _, c := range conditions {
			condition := c.(map[string]any)
			if condition["type"] == "Ready" {
				return condition["status"] == "True", nil
			}
		}
		return false, nil
	}).
		WithTimeout(15 * time.Minute).
		WithPolling(10 * time.Second).
		Should(BeTrue())
}

// gatewayHTTPRouteTests validates the HTTP-to-HTTPS redirect route is properly configured
// to redirect port 80 traffic to port 443 with a 301 status code.
func gatewayHTTPRouteTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking HTTPRoute giantswarm-default-tls-redirect exists in envoy-gateway-system")
	httpRoute := &unstructured.Unstructured{}
	httpRoute.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "HTTPRoute",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "giantswarm-default-tls-redirect",
			Namespace: "envoy-gateway-system",
		}, httpRoute)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking HTTPRoute parentRefs[0].name=giantswarm-default, sectionName=http")
	routeSpec := httpRoute.Object["spec"].(map[string]any)
	parentRefs := routeSpec["parentRefs"].([]any)
	Expect(parentRefs).NotTo(BeEmpty())
	parentRef := parentRefs[0].(map[string]any)
	Expect(parentRef["name"]).To(Equal("giantswarm-default"))
	Expect(parentRef["sectionName"]).To(Equal("http"))

	By("checking HTTPRoute rules[0] redirects to https with 301")
	rules := routeSpec["rules"].([]any)
	Expect(rules).NotTo(BeEmpty())
	rule := rules[0].(map[string]any)
	filters := rule["filters"].([]any)
	Expect(filters).NotTo(BeEmpty())
	filter := filters[0].(map[string]any)
	requestRedirect := filter["requestRedirect"].(map[string]any)
	Expect(requestRedirect["scheme"]).To(Equal("https"))
	Expect(requestRedirect["statusCode"]).To(BeEquivalentTo(301))
}
