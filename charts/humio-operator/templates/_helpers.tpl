{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "humio.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "humio.labels" -}}
app: '{{ .Chart.Name }}'
app.kubernetes.io/name: '{{ .Chart.Name }}'
app.kubernetes.io/instance: '{{ .Release.Name }}'
app.kubernetes.io/managed-by: '{{ .Release.Service }}'
helm.sh/chart: '{{ include "humio.chart" . }}'
{{- if .Values.commonLabels }}
{{ toYaml .Values.commonLabels }}
{{- end }}
{{- end }}