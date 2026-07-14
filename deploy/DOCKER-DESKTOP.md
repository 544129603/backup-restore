# Docker Desktop 本地部署记录

## 当前安装

本配置已实际安装到 kubeconfig context `docker-desktop`：

| 项目 | 当前值 |
|---|---|
| Kubernetes | v1.36.1，amd64，多节点 Docker Desktop |
| cert-manager | Helm Release `cert-manager/cert-manager`，v1.21.0 |
| Operator | Helm Release `backup-system/backup-restore`，Chart 0.1.0 |
| Operator 镜像 | `backup-restore-operator:dev-local-1` |
| 执行节点 | `desktop-worker2` |
| clusterRef | `docker-desktop` |
| Local Repo 宿主目录 | `/var/lib/backup-restore-operator/repository` |
| Local Repo 容器目录 | `/repository` |
| Webhook | 启用，证书由 cert-manager 管理 |

可重复安装配置位于 `deploy/docker-desktop-values.yaml`：

```powershell
docker build -t backup-restore-operator:dev-local-1 .

helm upgrade --install backup-restore charts/backup-restore-operator `
  --namespace backup-system --create-namespace `
  -f deploy/docker-desktop-values.yaml `
  --wait --timeout 5m
```

若是全新集群，应先按 cert-manager 官方方式安装依赖：

```powershell
helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager `
  --version v1.21.0 `
  --namespace cert-manager --create-namespace `
  --set crds.enabled=true `
  --wait --timeout 8m
```

## 已创建的本地验证对象

| 类型 | 名称 | 结果 |
|---|---|---|
| BackupPluginConfig | `cluster` | Ready |
| BackupRepository | `docker-desktop-local` | Ready；启用 AES-256-GCM |
| BackupScope | `docker-desktop-demo` | Ready；1 Namespace、3 对象 |
| BackupPolicy | `docker-desktop-demo-daily` | Paused；默认不自动产生副本 |
| BackupTask | `docker-desktop-smoke-20260713` | Completed |
| BackupRecord | 由上述 Task 生成 | Available、restorable、SHA-256 验证通过 |
| RestoreTask | `docker-desktop-restore-20260713` | Failed；用于验证 `Fail` 冲突策略 |
| RestoreTask | `docker-desktop-restore-skip-20260713` | Completed；创建 3、跳过 1 |

验证 Namespace：

- 源：`backup-demo`；
- 成功恢复目标：`backup-demo-restored-ok`；
- `demo-config` 与 `demo-secret` 已完成源/目标内容比对；
- `kube-root-ca.crt` 由目标 Namespace 自动创建，恢复时按 `Skip` 策略跳过。

## 当前环境限制

- Docker Desktop 当前没有 `snapshot.storage.k8s.io/v1` API、CSI Driver 或 VolumeSnapshotClass，因此 PVC Snapshot 预检查会返回不支持；这不影响 Kubernetes 资源备份。
- Kubernetes v1.36.1 高于当前代码已自动化验证的 v1.28–v1.32 范围。本次 Repository、Admission、备份、校验和恢复冒烟均通过，但仍需补充 v1.36 回归矩阵。
- `desktop-worker` 当前为 NotReady；Operator 已固定到健康的 `desktop-worker2`。
- Local Repo 绑定单节点，节点或 Docker Desktop 数据盘丢失会同时丢失副本；重要数据应改用 SFTP。
- `Fail` 冲突验证任务的 `processed` 显示为 5/4，说明 FailFast 状态持久化重试路径仍有一次重复计数；不影响已完成的 `Skip` 恢复和数据一致性结果，但属于后续需要修复的 UI/状态准确性缺陷。

## 日常检查

```powershell
helm list -A
kubectl get pods -n backup-system
kubectl get backuppluginconfig,backuprepository,backupscope,backuppolicy,backuptask,backuprecord,restoretask
kubectl logs deployment/backup-restore-operator -n backup-system --tail=200
```

启用每日策略：

```powershell
kubectl patch backuppolicy docker-desktop-demo-daily --type merge `
  -p '{"spec":{"enabled":true,"suspend":false}}'
```

## 清理

以下操作会删除本次验证数据，只应在不再需要副本时执行：

```powershell
kubectl delete restoretask docker-desktop-restore-20260713 docker-desktop-restore-skip-20260713
kubectl delete backuppolicy docker-desktop-demo-daily
kubectl delete backuptask docker-desktop-smoke-20260713

$record = kubectl get backuprecord -l protection.platform.io/project=demo -o jsonpath='{.items[0].metadata.name}'
kubectl annotate backuprecord $record `
  protection.platform.io/delete-confirmed=true `
  protection.platform.io/delete-mode=RepositoryData --overwrite
kubectl delete backuprecord $record

kubectl delete backupscope docker-desktop-demo
kubectl annotate backuprepository docker-desktop-local protection.platform.io/force-delete=true --overwrite
kubectl delete backuprepository docker-desktop-local
kubectl delete secret backup-encryption-v1 -n backup-system
kubectl delete namespace backup-demo backup-demo-restored backup-demo-restored-ok
helm uninstall backup-restore -n backup-system
```

Helm 不删除 `crds/` 中的 CRD。确认集群中没有其他插件数据后，才可显式删除 `protection.platform.io` CRD。cert-manager 也只应在确认没有其他应用依赖时单独卸载。
