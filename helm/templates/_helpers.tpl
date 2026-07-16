{{/*
Expand the name of the chart.
*/}}
{{- define "clawhermes-ai-go.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "clawhermes-ai-go.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "clawhermes-ai-go.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "clawhermes-ai-go.labels" -}}
helm.sh/chart: {{ include "clawhermes-ai-go.chart" . }}
{{ include "clawhermes-ai-go.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "clawhermes-ai-go.selectorLabels" -}}
app.kubernetes.io/name: {{ include "clawhermes-ai-go.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "clawhermes-ai-go.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "clawhermes-ai-go.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Expand the name of the chart.
*/}}
{{- define "stratum.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "stratum.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "stratum.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "stratum.labels" -}}
helm.sh/chart: {{ include "stratum.chart" . }}
{{ include "stratum.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "stratum.selectorLabels" -}}
app.kubernetes.io/name: {{ include "stratum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "stratum.serviceAccountName" -}}
{{- $defaultName := include "stratum.fullname" . -}}
{{- with .Values.serviceAccount -}}
{{- if .create -}}
{{- default $defaultName .name }}
{{- else -}}
{{- default "default" .name }}
{{- end -}}
{{- else -}}
default
{{- end -}}
{{- end }}

{{/* Render an immutable digest when supplied, otherwise preserve tag-based workflows. */}}
{{- define "stratum.image" -}}
{{- if .digest -}}
{{ printf "%s@%s" .repository .digest }}
{{- else -}}
{{ printf "%s:%s" .repository .tag }}
{{- end -}}
{{- end }}

{{/*
Resolve ingress service names.
*/}}
{{- define "stratum.ingressServiceName" -}}
{{- $root := .root -}}
{{- if eq .service "backend" -}}
{{ include "stratum.fullname" $root }}
{{- else -}}
{{ include "stratum.fullname" $root }}-frontend
{{- end -}}
{{- end }}

{{/*
Resolve ingress service ports.
*/}}
{{- define "stratum.ingressServicePort" -}}
{{- $root := .root -}}
{{- if eq .service "backend" -}}
{{ $root.Values.app.service.port }}
{{- else -}}
{{ $root.Values.frontend.service.port }}
{{- end -}}
{{- end }}
