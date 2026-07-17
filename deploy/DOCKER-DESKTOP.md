# Docker Desktop 本地部署与验收记录

本文记录 2026-07-14 在本机 `docker-desktop` Kubernetes 集群完成的实际部署和验收结果，可作为重复部署手册使用。

## 1. 当前部署状态

| 项目 | 当前值 |
|---|---|
| Kubernetes Context | `docker-desktop` |
| Kubernetes | v1.36.1，单节点 `desktop-control-plane` Ready |
| Helm Release | `backup-system/backup-restore`，revision 1（全新安装） |
| Operator 镜像 | `backup-restore-operator:dev-local-11` |
| Operator Pod | 1/1 Ready，启用 Leader Election 和 Admission Webhook |
| CRD | 6 个，全部 Cluster-scoped、Established；选择范围已合并进 BackupPolicy |
| clusterRef | `docker-desktop`，六个业务 CRD 均必填 |
| Local Repo | 节点 `/var/lib/backup-restore-operator/repository` 挂载到容器 `/repository` |
| 工作目录 | PVC `backup-system/backup-restore-workspace`，20Gi、Bound |
| Webhook TLS | cert-manager 管理，Certificate Ready |
| Web 管理界面 | 独立 Deployment 1/1 Ready；本机入口 `http://localhost:8082`；提供查询、创建、修改四步向导 |

当前 API 已删除旧项目归属字段和租户归属字段。本版本按单集群管理员、单租户模式部署；`clusterRef` 只用于路由和同集群引用校验，不是授权字段。普通用户不得直接访问 Cluster-scoped CRD。

## 2. 重复部署

确认 Docker Desktop Kubernetes 已启用，并安装 cert-manager 后执行：

```powershell
kubectl config use-context docker-desktop
kubectl get nodes

docker build --pull=false --build-arg VERSION=dev-local-11 -t backup-restore-operator:dev-local-11 .

# Helm upgrade 不会升级 chart crds/，必须显式执行。
kubectl apply -k config/crd/bases

kubectl apply -f deploy/docker-desktop-workspace-pvc.yaml

helm upgrade --install backup-restore charts/backup-restore-operator `
  --namespace backup-system --create-namespace `
  -f deploy/docker-desktop-values.yaml `
  --wait --timeout 8m

kubectl rollout status deployment/backup-restore-operator `
  -n backup-system --timeout=240s

kubectl rollout status deployment/backup-restore-operator-webui `
  -n backup-system --timeout=240s

Set-ExecutionPolicy -Scope Process Bypass -Force
.\deploy\start-webui.ps1
# 浏览器访问 http://localhost:8082
```

全新集群可先安装 cert-manager：

```powershell
helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager `
  --version v1.21.0 `
  --namespace cert-manager --create-namespace `
  --set crds.enabled=true `
  --wait --timeout 8m
```

## 3. 部署校验

```powershell
kubectl get pods,pvc,certificate -n backup-system
kubectl get crd | Select-String protection.platform.io
kubectl api-resources --api-group=protection.platform.io
kubectl get backuppluginconfig,backuprepository,backuppolicy,backuptask,backuprecord,restoretask
kubectl logs deployment/backup-restore-operator -n backup-system --tail=200
```

API Schema 门禁：

- 五个业务 CRD 的 `spec.clusterRef` 存在且必填。
- CRD、嵌套的 `BackupTask.spec.selectionSnapshot`、样例和备份包元数据均使用 Policy 内嵌 selection。
- 旧字段请求由 API Server strict decoding 拒绝。
- 缺少 `clusterRef` 的请求由 CRD Schema 拒绝。

## 4. 本次真实业务验收

本次保留以下验证对象，便于继续查看：

| 类型 | 名称 | 结果 |
|---|---|---|
| BackupRepository | `docker-desktop-local` | Ready，Local hostPath，AES-256-GCM |
| BackupPolicy | `docker-desktop-e2e-schedule` | selection 预览为 1 Namespace、2 类资源、3 个对象，成功生成计划任务后已暂停 |
| BackupTask | `docker-desktop-e2e-backup` | Completed |
| BackupRecord | `backup-9cf48c54-811a-45f5-85ea-0f49a6eff14a` | Available、Restorable、1,989 bytes |
| RestoreTask | `docker-desktop-e2e-dryrun` | Completed，4 项计划，未创建资源 |
| RestoreTask | `docker-desktop-e2e-restore` | Completed，创建 3、跳过 1、失败 0 |
| Scheduled BackupTask | `docker-desktop-e2e-schedule-1783998420` | Completed |

恢复数据核对结果：

- 源 Namespace：`backup-e2e-source`。
- 目标 Namespace：`backup-e2e-restored`。
- `ConfigMap/backup-e2e-config` 的数据一致。
- `Secret/backup-e2e-secret` 的 Base64 数据一致。
- 目标 Namespace 自动生成的 `kube-root-ca.crt` 按 `Skip` 冲突策略跳过。
- `pvc.enabled: false` 冻结到 BackupTask 后仍为 `false`。
- Repo 中存在 `metadata.json`、`index.json`、`resources.tar.gz`、`snapshots.json`、`sha256sum.txt` 和 `.done`；归档 SHA-256 校验通过。

## 5. 已执行质量门禁

以下命令已在 Go 1.23.12 Linux 容器或本机集群真实执行并通过：

```text
go vet ./...
go test ./...
go build -trimpath ./cmd/manager
RUN_ENVTEST=1 go test ./test/integration -count=1 -v
helm lint charts/backup-restore-operator
helm template ...
kubectl kustomize config/default
docker build --build-arg VERSION=dev-local-11 -t backup-restore-operator:dev-local-11 .
```

EnvTest 使用 Kubernetes 1.32.0 API Server 二进制，RepositoryController 已在真实 EnvTest API Server 中进入 Ready。

## 6. 当前环境限制

- Docker Desktop 当前没有 `snapshot.storage.k8s.io/v1` API、Snapshot Controller、支持快照的 CSI Driver 或 VolumeSnapshotClass，因此 PVC Snapshot/Restore 未通过真实 E2E；本次只验收 Kubernetes 资源与 Secret 加密归档恢复。
- `standard/hostpath` 由 `rancher.io/local-path` 提供，不具备 CSI Snapshot 能力。
- Local Repo 绑定单节点，重置 Docker Desktop、删除节点或清理数据盘会丢失副本；重要数据应使用 SFTP 或后续对象存储后端。
- SFTP Adapter 的嵌入式 SSH/SFTP 单元测试通过，但本机未部署独立外部 SFTP 服务，未声明外部 SFTP E2E 通过。
- Kubernetes v1.36.1 高于当前自动化兼容矩阵 v1.28-v1.32；本次实际冒烟通过不等于完整兼容认证。
- 当前发行物包含本地管理员 Web UI 和集群内管理 API，但没有登录层或多租户 ACL；仅允许通过本机端口转发供集群管理员使用，不得直接暴露到不可信网络。
- Restore 逐对象断点、自动重试、Rename 重入幂等和 CRD Established 分阶段恢复仍是生产化风险，不建议直接用于无人值守的大规模生产恢复。

## 7. 日常操作

查看状态：

```powershell
helm list -A
kubectl get pods,pvc -n backup-system
kubectl get backuprepository,backuppolicy,backuptask,backuprecord,restoretask
kubectl logs deployment/backup-restore-operator -n backup-system --tail=200
```

重新启用示例定时策略：

```powershell
kubectl edit backuppolicy docker-desktop-e2e-schedule
# 将 spec.suspend 改为 false；验证后应再次暂停，避免每分钟生成副本。
```

## 8. 清理烟测数据

以下操作会删除本次验证副本和恢复数据，只应在不再需要验证记录时执行：

```powershell
kubectl delete restoretask docker-desktop-e2e-dryrun docker-desktop-e2e-restore
kubectl delete backuppolicy docker-desktop-e2e-schedule
kubectl delete backuptask --all

$records = kubectl get backuprecord -o name
foreach ($record in $records) {
  kubectl annotate $record `
    protection.platform.io/delete-confirmed=true `
    protection.platform.io/delete-mode=RepositoryData --overwrite
  kubectl delete $record
}

kubectl annotate backuprepository docker-desktop-local `
  protection.platform.io/force-delete=true --overwrite
kubectl delete backuprepository docker-desktop-local
kubectl delete secret backup-encryption-e2e -n backup-system
kubectl delete namespace backup-e2e-source backup-e2e-restored
```

卸载 Operator 前应先确认所有 BackupRecord 已按预期处理。Helm 不删除 CRD 和 CRD 数据，CRD 不应在共享集群中自动删除。
