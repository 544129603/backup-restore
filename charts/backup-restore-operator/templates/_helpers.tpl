{{- define "backup-restore.name" -}}backup-restore-operator{{- end -}}
{{- define "backup-restore.labels" -}}
app.kubernetes.io/name: {{ include "backup-restore.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
{{- define "backup-restore.serviceAccountName" -}}
{{- default "backup-restore-operator" .Values.serviceAccount.name -}}
{{- end -}}
