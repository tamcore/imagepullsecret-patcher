{{/*
Expand the name of the chart.
*/}}
{{- define "imagepullsecret-patcher.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "imagepullsecret-patcher.fullname" -}}
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
{{- define "imagepullsecret-patcher.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "imagepullsecret-patcher.labels" -}}
helm.sh/chart: {{ include "imagepullsecret-patcher.chart" . }}
{{ include "imagepullsecret-patcher.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "imagepullsecret-patcher.selectorLabels" -}}
app.kubernetes.io/name: {{ include "imagepullsecret-patcher.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "imagepullsecret-patcher.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "imagepullsecret-patcher.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Full image name concatenated from registry, repository and tag
*/}}
{{- define "imagepullsecret-patcher.fullImageName" -}}
{{- printf "%s%s%s:%s"
    (default "" .Values.image.registry)
    (ternary "/" "" (ne .Values.image.registry ""))
    .Values.image.repository
    (default .Chart.AppVersion .Values.image.tag)
}}
{{- end -}}