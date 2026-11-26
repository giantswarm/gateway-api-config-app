# AWS NLB readiness listener

On CAPA with an NLB, the NLB health check targets envoy's own `/healthz`. With PROXY protocol enabled it cannot probe the health check node port instead, because the NLB sends the PROXY header on health checks too. Envoy Gateway serves the pod readiness probe (`19003 /ready`) from the same health check filter, so both fail together when a proxy pod shuts down. With `externalTrafficPolicy: Local`, the node then has no serving proxy and refuses new connections until the NLB marks it unhealthy, about 20 to 30s later.

The chart therefore adds a listener on port 19004 that answers `/ready` with a 200 straight from envoy. It's added through the EnvoyProxy `bootstrap` (`type: Merge`), which appends it to Envoy Gateway's listeners. The envoy readiness probe moves to it through the chart's `envoyDeployment.patch`, while the startup and liveness probes keep `19003 /ready`. A terminating proxy pod stays ready and keeps serving its node during the drain. `/healthz` fails 1s after the pod starts terminating (the health check failure delay), so the NLB stops sending new connections to the node while envoy still accepts them.

Port 19004 is reserved: the chart fails the render when a gateway or listener set listener uses it.

## When the chart leaves it out

The chart leaves out the listener and the probe, and keeps a 30s health check failure delay instead, when:

- the gateway sets `provider.aws.readinessListener: false`
- the gateway or its GatewayClass sets `bootstrap`, since the chart cannot merge two bootstraps
- the GatewayClass sets `envoyDeployment.patch`, since Envoy Gateway cannot merge two `StrategicMerge` patches
- the gateway's own patch sets `containers` or a type other than `StrategicMerge`, which would replace the probe

Without the readiness listener, the node refuses new connections for about 20 to 30s each time a proxy pod terminates.

## GatewayClasses managed outside the chart

The chart only sees the GatewayClasses it manages itself. When a gateway's `className` points at a GatewayClass managed elsewhere whose EnvoyProxy sets `envoyDeployment.patch` or `bootstrap`, set `provider.aws.readinessListener: false` on the gateway. Otherwise the chart's patch or bootstrap replaces the class one: Envoy Gateway stops reconciling the gateway with two `StrategicMerge` patches, and a class `bootstrap` is overridden by the chart's.
