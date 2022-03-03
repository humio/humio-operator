{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "operator.chart" -}}
  {{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Name of the operator which is used to construct operator.fullname.
*/}}
{{- define "operator.name" -}}
  {{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Construct fullname used to name resources created by the chart.
*/}}
{{- define "operator.fullname" -}}
  {{- if .Values.fullnameOverride -}}
    {{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
  {{- else -}}
    {{- $name := default .Chart.Name .Values.nameOverride -}}
    {{- if contains $name .Release.Name -}}
      {{- .Release.Name | trunc 63 | trimSuffix "-" -}}
    {{- else -}}
      {{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
