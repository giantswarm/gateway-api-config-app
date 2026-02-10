{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "name" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "labels.common" -}}
app.kubernetes.io/name: {{ include "name" . | quote }}
application.giantswarm.io/team: {{ index .Chart.Annotations "io.giantswarm.application.team" | quote }}
giantswarm.io/managed-by: {{ .Release.Name | quote }}
helm.sh/chart: {{ include "chart" . | quote }}
{{- end -}}

{{/*
Gateway Service annotations
*/}}
{{- define "service.annotations" -}}
{{- $service := .gateway.service }}
{{- $annotations := dict }}

{{- /* Enable External-DNS */}}
{{- $_ := set $annotations "external-dns.alpha.kubernetes.io/hostname" (printf "%s.%s" .gateway.dnsName (.gateway.overrideBaseDomain | default .baseDomain)) }}
{{- $_ := set $annotations "giantswarm.io/external-dns" "managed" }}

{{- /* Use AWS NLB */}}
{{- if and (eq .provider "capa") (.gateway.provider.aws.useNetworkLoadBalancer) }}
{{- /* Enable PROXY Protocol */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-proxy-protocol" "*" }}

{{- /* Configure Health Checks on port 80 for all listeners */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-port" "http-80" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-path" "/healthz" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-healthy-threshold" "2" }}

{{- /* Make LB public by default */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-scheme" "internet-facing" }}

{{- /* Configure attributes */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-attributes" "load_balancing.cross_zone.enabled=true" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-target-group-attributes" "target_health_state.unhealthy.connection_termination.enabled=false,target_health_state.unhealthy.draining_interval_seconds=200,preserve_client_ip.enabled=false" }}
{{- end }}

{{- $annotations = mergeOverwrite $annotations (deepCopy (default dict $service.annotations)) }}
{{- $annotations | toYaml }}
{{- end }}

{{/*
Gateway Service loadBalancerClass
*/}}
{{- define "service.loadBalancerClass" -}}
{{- $service := .gateway.service }}
{{- if and (eq .provider "capa") (.gateway.provider.aws.useNetworkLoadBalancer) }}
{{- default "service.k8s.aws/nlb" $service.loadBalancerClass }}
{{- else }}
{{- default "" $service.loadBalancerClass }}
{{- end }}
{{- end }}

{{/*
Gateway Service externalTrafficPolicy
*/}}
{{- define "service.externalTrafficPolicy" -}}
{{- $service := .gateway.service }}
{{- if and (eq .provider "capa") (.gateway.provider.aws.useNetworkLoadBalancer) }}
{{- default "Local" $service.externalTrafficPolicy }}
{{- else }}
{{- default "Cluster" $service.externalTrafficPolicy }}
{{- end }}
{{- end }}

{{/*
Gateway EnvoyService defaults - computes provider-specific envoyService configuration
*/}}
{{- define "gateway.envoyServiceDefaults" -}}
{{- $envoyService := dict }}
{{- $_ := set $envoyService "loadBalancerClass" (include "service.loadBalancerClass" .) }}
{{- $_ := set $envoyService "externalTrafficPolicy" (include "service.externalTrafficPolicy" .) }}
{{- $_ := set $envoyService "annotations" ((include "service.annotations" .) | fromYaml) }}
{{- if .gateway.service.labels }}
{{- $_ := set $envoyService "labels" ((tpl (.gateway.service.labels | toYaml | toString) .root) | fromYaml) }}
{{- end }}
{{- $envoyService | toYaml }}
{{- end }}

{{/*
Gateway Shutdown defaults - computes provider-specific shutdown configuration
*/}}
{{- define "gateway.shutdownDefaults" -}}
{{- $shutdown := dict }}
{{- /* Set defaults for AWS NLBs */}}
{{- if and (eq .provider "capa") (.gateway.provider.aws.useNetworkLoadBalancer) }}
{{- $_ := set $shutdown "drainTimeout" "180s" }}
{{- $_ := set $shutdown "minDrainDuration" "60s" }}
{{- end }}
{{- $shutdown | toYaml }}
{{- end }}

{{/*
EnvoyProxy spec - shared spec output for EnvoyProxy resources
Takes: envoyProxyValues (dict with all the envoyProxy configuration)
*/}}
{{- define "envoyProxy.spec" -}}
provider:
  type: Kubernetes
  kubernetes:
    {{- with .envoyDeployment }}
    envoyDeployment:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyService }}
    envoyService:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyHpa }}
    envoyHpa:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyPDB }}
    envoyPDB:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyServiceAccount }}
    envoyServiceAccount:
      {{- toYaml . | nindent 6 }}
    {{- end }}
{{- with .logging }}
logging:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .telemetry }}
telemetry:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .bootstrap }}
bootstrap:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .concurrency }}
concurrency:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .extraArgs }}
extraArgs:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .mergeGateways }}
mergeGateways:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .shutdown }}
shutdown:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
