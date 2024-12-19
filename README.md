[![CircleCI](https://dl.circleci.com/status-badge/img/gh/giantswarm/envoy-gateway-default-configuration/tree/main.svg?style=svg)](https://dl.circleci.com/status-badge/redirect/gh/giantswarm/envoy-gateway-default-configuration/tree/main)

# envoy-gateway-default-configuration chart

Giant Swarm offers a `envoy-gateway-default-configuration` App, which can be installed in workload clusters together with [envoy-gateway-app](https://github.com/giantswarm/envoy-gateway-app).
Here, we define the `envoy-gateway-default-configuration` chart with its templates and default configuration.

**What is this app?**

**Why did we add it?**

**Who can use it?**

## Installing

There are several ways to install this app onto a workload cluster.

- [Using GitOps to instantiate the App](https://docs.giantswarm.io/advanced/gitops/apps/)
- [Using our web interface](https://docs.giantswarm.io/platform-overview/web-interface/app-platform/#installing-an-app).
- By creating an [App resource](https://docs.giantswarm.io/use-the-api/management-api/crd/apps.application.giantswarm.io/) in the management cluster as explained in [Getting started with App Platform](https://docs.giantswarm.io/getting-started/app-platform/).

## Configuring

### values.yaml

**This is an example of a values file you could upload using our web interface.**

```yaml
# values.yaml

```

### Sample App CR and ConfigMap for the management cluster

If you have access to the Kubernetes API on the management cluster, you could create
the App CR and ConfigMap directly.

Here is an example that would install the app to
workload cluster `abc12`:

```yaml
# appCR.yaml

```

```yaml
# user-values-configmap.yaml

```

See our [full reference on configuring apps](https://docs.giantswarm.io/getting-started/app-platform/app-configuration/) for more details.

## Compatibility

This app has been tested to work with the following workload cluster release versions:

- _add release version_

## Limitations

Some apps have restrictions on how they can be deployed.
Not following these limitations will most likely result in a broken deployment.

- _add limitation_

## Credit

- [Giant Swarm Catalog](https://github.com/giantswarm/giantswarm-catalog)
