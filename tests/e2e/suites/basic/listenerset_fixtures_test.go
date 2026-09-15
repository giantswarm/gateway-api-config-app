package basic

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/application"
	"github.com/giantswarm/clustertest/v5/pkg/client"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	gatewayNamespace = "envoy-gateway-system"
	gatewayName      = "giantswarm-default"

	// chartListenerSetName is what the chart renders for gateways.default.listenerSets.extra,
	// and chartListenerSetPrefix the name its supporting resources are keyed on.
	chartListenerSetName   = "giantswarm-default-extra"
	chartListenerSetPrefix = "gateway-giantswarm-default-extra"
	chartListenerSetHost   = "lset-chart"

	// The tenant fixture lives entirely outside the chart: a namespace a customer
	// would own, holding its own ListenerSet, Certificate, backend and routes.
	tenantNamespace       = "e2e-lset-tenant"
	tenantListenerSetName = "e2e-lset-tenant"
	tenantCertName        = "lset-tenant"
	tenantSecretName      = "lset-tenant-tls"
	tenantListenerSetHost = "lset-tenant"

	// The backend both fixtures route to. /status/200 is the happy path,
	// /status/503 the signal for the error-page policies.
	httpbinName  = "httpbin"
	httpbinImage = "gsoci.azurecr.io/giantswarm/go-httpbin:2.23.1"
	httpbinPort  = 8080

	// Marker header each HTTPRoute adds to its responses, so a 200 proves which
	// route, and therefore which listener, served the request.
	routeMarkerHeader = "X-E2E-Route"

	// Error page bodies, kept disjoint so a response body identifies the policy
	// that produced it. They must stay in sync with bundle_values.yaml.
	gatewayErrorPageMarker     = "Giant Swarm - Service Unavailable"
	listenerSetErrorPageMarker = "ListenerSet Error Page"
)

// wcZone returns the workload cluster's DNS zone, which is what the chart is
// installed with as .Values.baseDomain.
func wcZone() string {
	values := &application.ClusterValues{}
	err := state.GetFramework().MC().GetHelmValues(state.GetCluster().Name, state.GetCluster().GetNamespace(), values)
	Expect(err).NotTo(HaveOccurred())
	Expect(values.BaseDomain).NotTo(BeEmpty(), "baseDomain missing from cluster helm values")

	return fmt.Sprintf("%s.%s", state.GetCluster().Name, values.BaseDomain)
}

func chartListenerSetHostname() string  { return fmt.Sprintf("%s.%s", chartListenerSetHost, wcZone()) }
func tenantListenerSetHostname() string { return fmt.Sprintf("%s.%s", tenantListenerSetHost, wcZone()) }

// gatewayHostname is the apex the chart puts on the envoy Service, used as the
// control request in the policy cascade matrix.
func gatewayHostname() string { return fmt.Sprintf("ingress.%s", wcZone()) }

func wc() *client.Client {
	wcClient, err := state.GetFramework().WC(state.GetCluster().Name)
	Expect(err).NotTo(HaveOccurred())
	return wcClient
}

// createIfMissing creates obj and tolerates an existing one, so every fixture
// step is safe to re-enter.
func createIfMissing(obj cr.Object) {
	err := wc().Create(state.GetContext(), obj)
	if err != nil && !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "failed to create %T %s/%s", obj, obj.GetNamespace(), obj.GetName())
	}
}

// ptrTo is the usual helper for the many optional pointer fields in the Kubernetes
// and Gateway API types below.
func ptrTo[T any](v T) *T { return &v }

// createTenantNamespace and deployHttpbin bring up the backend both fixtures share.
func createTenantNamespace() {
	By("creating the tenant namespace " + tenantNamespace)
	createIfMissing(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tenantNamespace}})
}

// deployHttpbin runs go-httpbin with the restricted-PSS securityContext our
// Kyverno policies require, so the fixture is not blocked at admission.
func deployHttpbin() {
	podLabels := map[string]string{"app": httpbinName}

	By("deploying go-httpbin into " + tenantNamespace)
	createIfMissing(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: httpbinName, Namespace: tenantNamespace, Labels: podLabels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrTo(int32(2)),
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptrTo(true),
						RunAsUser:      ptrTo(int64(1000)),
						RunAsGroup:     ptrTo(int64(1000)),
						FSGroup:        ptrTo(int64(1000)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  httpbinName,
						Image: httpbinImage,
						Args:  []string{fmt.Sprintf("-port=%d", httpbinPort)},
						Ports: []corev1.ContainerPort{{ContainerPort: httpbinPort}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path: "/status/200",
								Port: intstr.FromInt32(httpbinPort),
							}},
							InitialDelaySeconds: 5,
							PeriodSeconds:       10,
						},
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             ptrTo(true),
							RunAsUser:                ptrTo(int64(1000)),
							RunAsGroup:               ptrTo(int64(1000)),
							AllowPrivilegeEscalation: ptrTo(false),
							ReadOnlyRootFilesystem:   ptrTo(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	})

	createIfMissing(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: httpbinName, Namespace: tenantNamespace, Labels: podLabels},
		Spec: corev1.ServiceSpec{
			Selector: podLabels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       httpbinPort,
				TargetPort: intstr.FromInt32(httpbinPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	})

	By("waiting for go-httpbin to become ready")
	Eventually(func() error {
		dep := &appsv1.Deployment{}
		if err := wc().Get(state.GetContext(), cr.ObjectKey{Name: httpbinName, Namespace: tenantNamespace}, dep); err != nil {
			return err
		}
		if dep.Status.ReadyReplicas < 1 {
			return fmt.Errorf("go-httpbin has %d ready replicas", dep.Status.ReadyReplicas)
		}
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// createTenantListenerSet creates the tenant's ListenerSet while its TLS Secret
// does not exist yet. The Certificate follows in createTenantCertificate; that
// ordering is the envoyproxy/gateway#9614 regression this suite locks in, so the
// two steps must stay separate.
func createTenantListenerSet() {
	By("creating the tenant ListenerSet before its TLS Secret exists")

	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	secretKind := gatewayv1.Kind("Secret")
	terminate := gatewayv1.TLSModeTerminate

	createIfMissing(&gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: tenantListenerSetName, Namespace: tenantNamespace},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Group:     &group,
				Kind:      &kind,
				Name:      gatewayv1.ObjectName(gatewayName),
				Namespace: ptrTo(gatewayv1.Namespace(gatewayNamespace)),
			},
			Listeners: []gatewayv1.ListenerEntry{{
				Name:     gatewayv1.SectionName("https"),
				Hostname: ptrTo(gatewayv1.Hostname(tenantListenerSetHostname())),
				Port:     gatewayv1.PortNumber(443),
				Protocol: gatewayv1.HTTPSProtocolType,
				TLS: &gatewayv1.ListenerTLSConfig{
					Mode: &terminate,
					CertificateRefs: []gatewayv1.SecretObjectReference{{
						Kind: &secretKind,
						Name: gatewayv1.ObjectName(tenantSecretName),
					}},
				},
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{
						From: ptrTo(gatewayv1.NamespacesFromSame),
					},
				},
			}},
		},
	})
}

// createTenantCertificate asks cert-manager for the tenant listener's certificate.
// Called only after createTenantListenerSet, see the note there.
func createTenantCertificate() {
	By("creating the tenant Certificate")
	createIfMissing(&cmv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: tenantCertName, Namespace: tenantNamespace},
		Spec: cmv1.CertificateSpec{
			DNSNames:   []string{tenantListenerSetHostname()},
			SecretName: tenantSecretName,
			IssuerRef: cmmeta.IssuerReference{
				Group: "cert-manager.io",
				Kind:  "ClusterIssuer",
				Name:  "letsencrypt-giantswarm",
			},
		},
	})
}

// createFixtureRoutes wires go-httpbin behind all three hostnames of the policy
// cascade matrix: the gateway apex (control), the chart ListenerSet, and the
// tenant ListenerSet.
func createFixtureRoutes() {
	By("creating the HTTPRoutes for the gateway, the chart ListenerSet and the tenant ListenerSet")

	// Only the tenant route carries the external-dns annotation. The other two
	// hostnames already have records (the envoy Service annotation for the gateway
	// apex, the chart DNSEndpoint for the chart ListenerSet), and letting the
	// gateway-httproute source claim them too would put two sources on the same
	// name with different record types.
	createIfMissing(httpbinRoute(
		"httpbin-gateway",
		gatewayHostname(),
		"gateway-control",
		gatewayv1.Kind("Gateway"), gatewayName, gatewayNamespace,
		false,
	))

	createIfMissing(httpbinRoute(
		"httpbin-lset-chart",
		chartListenerSetHostname(),
		"lset-chart",
		gatewayv1.Kind("ListenerSet"), chartListenerSetName, gatewayNamespace,
		false,
	))

	createIfMissing(httpbinRoute(
		"httpbin-lset-tenant",
		tenantListenerSetHostname(),
		"lset-tenant",
		gatewayv1.Kind("ListenerSet"), tenantListenerSetName, tenantNamespace,
		true,
	))
}

// httpbinRoute builds an HTTPRoute in the tenant namespace forwarding hostname to
// go-httpbin, stamping marker into every response.
//
// manageDNS adds the annotation our external-dns filters on
// (annotationFilter giantswarm.io/external-dns=managed); without it the
// gateway-httproute source skips the route and no record is ever created.
func httpbinRoute(name, hostname, marker string, parentKind gatewayv1.Kind, parentName, parentNamespace string, manageDNS bool) *gatewayv1.HTTPRoute {
	group := gatewayv1.Group(gatewayv1.GroupName)
	section := gatewayv1.SectionName("https")

	annotations := map[string]string{}
	if manageDNS {
		annotations["giantswarm.io/external-dns"] = "managed"
	}

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   tenantNamespace,
			Annotations: annotations,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Group:       &group,
					Kind:        &parentKind,
					Name:        gatewayv1.ObjectName(parentName),
					Namespace:   ptrTo(gatewayv1.Namespace(parentNamespace)),
					SectionName: &section,
				}},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
			Rules: []gatewayv1.HTTPRouteRule{{
				Filters: []gatewayv1.HTTPRouteFilter{{
					Type: gatewayv1.HTTPRouteFilterResponseHeaderModifier,
					ResponseHeaderModifier: &gatewayv1.HTTPHeaderFilter{
						Set: []gatewayv1.HTTPHeader{{
							Name:  gatewayv1.HTTPHeaderName(routeMarkerHeader),
							Value: marker,
						}},
					},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Kind: ptrTo(gatewayv1.Kind("Service")),
							Name: gatewayv1.ObjectName(httpbinName),
							Port: ptrTo(gatewayv1.PortNumber(httpbinPort)),
						},
					},
				}},
			}},
		},
	}
}

// cleanupListenerSetFixtures deletes the tenant namespace, taking the tenant
// ListenerSet, Certificate, Secret, routes and backend with it. Best effort: the
// workload cluster is torn down right after, so a failure here must not mask the
// real test result.
func cleanupListenerSetFixtures() {
	wcClient, err := state.GetFramework().WC(state.GetCluster().Name)
	if err != nil {
		logger.Log("Skipping ListenerSet fixture cleanup, no workload cluster client: %v", err)
		return
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tenantNamespace}}
	if err := wcClient.Delete(state.GetContext(), ns); err != nil && !errors.IsNotFound(err) {
		logger.Log("Failed to delete namespace %s: %v", tenantNamespace, err)
	}
}
