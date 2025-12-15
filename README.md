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
- **Graceful shutdown (180s drain, 60s min drain)**: Allows in-flight requests to complete during pod termination, aligning with NLB's deregistration delay for zero-downtime deployments.

## Credit

- [Giant Swarm Catalog](https://github.com/giantswarm/giantswarm-catalog)
