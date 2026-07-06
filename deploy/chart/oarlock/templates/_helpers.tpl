{{- define "oarlock.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "oarlock.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "oarlock.labels" -}}
app.kubernetes.io/name: {{ include "oarlock.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "oarlock.selectorLabels" -}}
app.kubernetes.io/name: {{ include "oarlock.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "oarlock.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{- define "oarlock.secretName" -}}
{{- .Values.existingSecret | default (include "oarlock.fullname" .) -}}
{{- end -}}

{{/* The component that serves HTTP (targeted by the Service/Ingress). */}}
{{- define "oarlock.webComponent" -}}
{{- if eq .Values.mode "simple" -}}all{{- else -}}api{{- end -}}
{{- end -}}

{{/* DATABASE_URL for the bundled postgres. */}}
{{- define "oarlock.bundledDatabaseUrl" -}}
{{- printf "postgres://%s:%s@%s-postgres:5432/%s" .Values.postgres.username .Values.postgres.password (include "oarlock.fullname" .) .Values.postgres.database -}}
{{- end -}}

{{/* VALKEY_ADDR: explicit value wins, else the bundled valkey when enabled. */}}
{{- define "oarlock.valkeyAddr" -}}
{{- if .Values.config.valkeyAddr -}}
{{- .Values.config.valkeyAddr -}}
{{- else if .Values.valkey.enabled -}}
{{- printf "%s-valkey:6379" (include "oarlock.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* Shared container env for every oarlock component. The bundled postgres
     wires DATABASE_URL directly (its credentials already live in values);
     an external database's URL comes from the (existing or chart) Secret. */}}
{{- define "oarlock.env" -}}
{{- if .Values.postgres.enabled }}
- name: DATABASE_URL
  value: {{ include "oarlock.bundledDatabaseUrl" . | quote }}
{{- else }}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ include "oarlock.secretName" . }}
      key: DATABASE_URL
{{- end }}
- name: OARLOCK_MASTER_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "oarlock.secretName" . }}
      key: OARLOCK_MASTER_KEY
{{- with (include "oarlock.valkeyAddr" .) }}
- name: VALKEY_ADDR
  value: {{ . | quote }}
{{- end }}
{{- with .Values.config.allowedOrigins }}
- name: OARLOCK_ALLOWED_ORIGINS
  value: {{ . | quote }}
{{- end }}
{{- with .Values.config.secureCookies }}
- name: OARLOCK_SECURE_COOKIES
  value: {{ . | quote }}
{{- end }}
{{- end -}}
