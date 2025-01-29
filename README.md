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

### values.yaml

**This is an example of a values file you could upload using our web interface.**

Configure the gateway name, class and host to your needs.

```yaml
# values.yaml
gateway:
  name: default
  class: default
  host: test.gaws.gigantic.io
```

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
  version: 0.1.0
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
    gateway:
      name: default
      class: default
      host: mycluster.gaws.gigantic.io
```

See our [full reference on configuring apps](https://docs.giantswarm.io/getting-started/app-platform/app-configuration/) for more details.

## Compatibility

This app has been tested to work with the following workload cluster release versions:

- 0.1.0

## Limitations

None

## Credit

- [Giant Swarm Catalog](https://github.com/giantswarm/giantswarm-catalog)
