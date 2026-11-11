[![CircleCI](https://dl.circleci.com/status-badge/img/gh/giantswarm/gateway-api-config-app/tree/main.svg?style=svg)](https://dl.circleci.com/status-badge/redirect/gh/giantswarm/gateway-api-config-app/tree/main)

# gateway-api-config chart

Giant Swarm offers a `gateway-api-config` App, which can be installed in workload clusters together with [envoy-gateway-app](https://github.com/giantswarm/envoy-gateway-app).

Here, we define the `gateway-api-config` chart with its templates and default configuration.

**What is this app?**

Gateway API Config is a Giant Swarm App that provides a default configuration for the Envoy Gateway App. It is a optional dependency for the Envoy Gateway App to have a default gateway configuration.

**Why did we add it?**

Since the default [envoy-gateway-app](https://github.com/giantswarm/envoy-gateway-app) only deploys the Envoy Gateway, it does not provide a default configuration for the gateway. This gives the user the flexibility to configure the gateway as needed to have a quick start.

**Who can use it?**

Any platform user who wants to have a default configuration for the Envoy Gateway App.

## Installing

There are several ways to install this app onto a workload cluster.

- [Using GitOps to instantiate the App](https://docs.giantswarm.io/advanced/gitops/apps/)
- [Using our web interface](https://docs.giantswarm.io/platform-overview/web-interface/app-platform/#installing-an-app).
- By creating an [App resource](https://docs.giantswarm.io/use-the-api/management-api/crd/apps.application.giantswarm.io/) in the management cluster as explained in [Getting started with App Platform](https://docs.giantswarm.io/getting-started/app-platform/).

## Configuring

### Sample App CR and ConfigMap for the management cluster

If you have access to the platform API on the management cluster, you could create the App CR and ConfigMap directly.

Here is an example that would install the app to workload cluster `abc12`:

```yaml
# appCR.yaml
apiVersion: application.giantswarm.io/v1alpha1
kind: App
metadata:
  labels:
    giantswarm.io/cluster: abc12
  name: gateway-api-config
  namespace: org-giantswarm
spec:
  catalog: giantswarm
  extraconfigs:
    - name: gateway-api-config-user-values
      namespace: org-giantswarm
  kubeConfig:
    inCluster: false
  name: gateway-api-config
  namespace: giantswarm
  version: 1.3.0
```

```yaml
# user-values-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gateway-api-config-user-values
  namespace: org-giantswarm
data:
  values: |
    gateways:
      default:
        hostnames:
          - test.<mycluster>.gaws.gigantic.io
```

See our [full reference on configuring apps](https://docs.giantswarm.io/getting-started/app-platform/app-configuration/) for more details.

## Resources

### GatewayClass

This chart deploys a `giantswarm-default` GatewayClass with an associated EnvoyProxy configuration.

The default EnvoyProxy settings apply security hardening:

- Runs as non-root user (UID/GID 65534)
- Read-only root filesystem
- All capabilities dropped
- Seccomp profile set to `RuntimeDefault`

### Gateway

This chart deploys a `giantswarm-default` Gateway using the `giantswarm-default` GatewayClass.

The default Gateway includes:

- **Listeners**: HTTP (port 80) and HTTPS (port 443) with wildcard hostname `*.{baseDomain}`
- **TLS**: Certificates managed via Let's Encrypt (`letsencrypt-giantswarm-gateway` issuer)
- **DNS**: Automatic DNS endpoint creation via external-dns
- **Autoscaling**: HPA with 2-10 replicas and PDB with 25% max unavailable
- **Observability**: PodLogs and PodMonitor enabled for logging and metrics

#### AWS Network Load Balancer (CAPA)

On CAPA clusters, the Gateway is configured with an AWS Network Load Balancer:

- **Proxy protocol enabled**: Preserves original client IP and port information through the load balancer, required for accurate client identification at the application layer.
- **`preserve_client_ip` disabled**: Avoids asymmetric routing issues when using proxy protocol; client IP is already conveyed via proxy protocol headers.
- **Cross-zone load balancing**: Distributes traffic evenly across all targets in all availability zones, improving fault tolerance and resource utilization.
- **Internet-facing scheme**: Exposes the Gateway publicly to accept traffic from the internet.
- **Health checks on `/healthz` (port 80)**: Uses Envoy's built-in health endpoint to ensure traffic is only routed to healthy pods.
- **Local external traffic policy**: Preserves the source IP at the node level and avoids extra network hops, reducing latency.
- **Graceful shutdown (30s health check failure delay, 180s minimum drain, 200s drain timeout, 240s termination grace period)**: Keeps `/healthz` passing for 30s after the pod starts terminating so the NLB stops sending new flows first, then lets in-flight requests finish while the NLB deregisters the target, for zero-downtime deployments. Requires Envoy Gateway 1.9.

##### Choosing the NLB subnets

The `service.beta.kubernetes.io/aws-load-balancer-subnets` annotation pins the NLB to a list of subnets, one for each availability zone (AZ). If you set it, the list must cover **every AZ that can run proxy pods**. With `externalTrafficPolicy: Local`, the NLB sends no traffic to a proxy pod in an AZ that is not on the list.

The chart fails the render when the list has an empty entry, such as `subnet-a,` or `subnet-a,,subnet-b`. The AWS Load Balancer Controller ignores empty entries, so the NLB would silently lose an AZ. Remove the extra comma.

To keep the NLB in fewer AZs than the cluster has, keep the proxy pods in the same AZs. Add a `nodeAffinity` on `topology.kubernetes.io/zone`. It is merged with the default `podAntiAffinity`, which stays in place:

```yaml
gateways:
  default:
    service:
      annotations:
        service.beta.kubernetes.io/aws-load-balancer-subnets: subnet-0aaa1111,subnet-0bbb2222  # eu-central-1a, eu-central-1b
    envoyProxy:
      envoyDeployment:
        pod:
          affinity:
            nodeAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                nodeSelectorTerms:
                  - matchExpressions:
                      - key: topology.kubernetes.io/zone
                        operator: In
                        values:
                          - eu-central-1a
                          - eu-central-1b
```

#### Azure load balancer (CAPZ and AKS)

On CAPZ and AKS clusters (both get `provider: capz`), the Gateway gets the same graceful shutdown timers as on CAPA: a 30s health check failure delay, a 180s minimum drain, a 200s drain timeout and a 240s termination grace period. Requires Envoy Gateway 1.9.

The Service keeps `externalTrafficPolicy: Cluster`. The Azure load balancer then probes the node port over TCP and never sees Envoy's `/healthz`. A terminating proxy pod leaves the Service endpoints at once, and the drain lets its established connections finish. Azure does not reset them. The 30s health check failure delay matters when the gateway or its GatewayClass sets `externalTrafficPolicy: Local`. The Azure probe then targets the health check node port, which fails as soon as the pod starts terminating. It needs time (every 5s, 2 probes by default) to stop sending new connections to the node. Envoy stays ready (its `/ready` readiness probe keeps passing) for those 30s, so kube-proxy keeps forwarding to the terminating pod until the Azure probe has taken the node out.

A GatewayClass EnvoyProxy that sets `shutdown` fields replaces these defaults field by field, as long as the gateway EnvoyProxy merges onto it (the default `mergeType: StrategicMerge`, or `JSONMerge`). With `mergeType: Replace` the defaults stay. The 240s termination grace period is left out when the GatewayClass or the gateway sets `shutdown.drainTimeout` or its own `envoyDeployment.patch`. Envoy Gateway then sets it to the drain timeout plus 300s. A patch you set yourself has to keep `terminationGracePeriodSeconds` above the drain timeout. The same applies on CAPA. The chart only sees GatewayClasses it manages itself. For a gateway whose `className` points at a GatewayClass managed elsewhere with its own patch, set `envoyProxy.mergeType: Replace` on the gateway, or set the patch on the gateway itself.

## Credit

- [Giant Swarm Catalog](https://github.com/giantswarm/giantswarm-catalog)
