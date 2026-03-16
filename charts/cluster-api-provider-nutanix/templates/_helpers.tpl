{{/* vim: set filetype=mustache: */}}

{{/*
Expand the name of the chart.
*/}}
{{- define "chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "chart.fullname" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "chart.labels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
helm.sh/chart: {{ include "chart.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
cluster.x-k8s.io/provider: infrastructure-nutanix
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels (used by Deployment + Service)
*/}}
{{- define "chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Certificate Issuer name.
Defaults to "<chart-name>-issuer" when selfSigned; otherwise .Values.certificates.issuer.name is required.
*/}}
{{- define "chart.issuerName" -}}
{{- if .Values.certificates.issuer.selfSigned -}}
  {{- if .Values.certificates.issuer.name -}}
    {{- .Values.certificates.issuer.name -}}
  {{- else -}}
    {{- printf "%s-issuer" (include "chart.name" .) -}}
  {{- end -}}
{{- else -}}
  {{- required "A valid .Values.certificates.issuer.name is required when selfSigned is false!" .Values.certificates.issuer.name -}}
{{- end -}}
{{- end -}}

{{/*
Admission TLS secret name (the cert-manager Certificate writes the keypair here).
*/}}
{{- define "chart.admissionTLSSecretName" -}}
{{- if .Values.certificates.admission.secretName -}}
  {{- .Values.certificates.admission.secretName -}}
{{- else -}}
  {{- printf "%s-admission-tls" (include "chart.name" .) -}}
{{- end -}}
{{- end -}}

{{/*
Image reference.
*/}}
{{- define "chart.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
