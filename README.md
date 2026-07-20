# Backup & Restore Operator

基于 Kubernetes Operator 的集群级备份与同集群恢复实现。API 为 `protection.platform.io/v1alpha1`，6 个 CRD 全部是 Cluster-scoped；业务 Namespace 删除不会级联删除 `BackupRecord`。

## 已实现能力

- Local（预挂载 hostPath/PVC 路径）与 SFTP Repository；SFTP 支持密码/私钥、`known_hosts`、超时、并发、keepalive、临时文件上传及原子改名。
- Cluster/Namespace 范围、GVR include/exclude、LabelSelector、Secret/CRD/CR 开关、资源清洗与范围预览。
- 5 字段 Cron、IANA 时区、Forbid/Replace/Allow、RunOnce/RunAll/Skip、确定性任务名及重启防重。
- Policy 计划备份与无需 Policy 的 OneTime 一次性备份；两者都固化完整执行配置并生成独立恢复点。
- 流式采集、确定性 `tar.gz`、SHA-256 manifest、可选分块 AES-256-GCM、`.done` 提交点、独立 Record 二次校验。
- CSI `VolumeSnapshot` 创建、等待、静态快照引用、同集群/跨 Namespace 恢复准备及生命周期删除。
- 恢复计划、DryRun、Skip/Overwrite/Rename/Fail、受控删除重建、Namespace/CRD/集群资源/PVC/命名空间资源顺序恢复。
- Finalizer、保留策略、任务 GC、取消/超时/重试、Conditions/Event/Prometheus 指标、Admission Webhook。

## 快速验证

```powershell
$env:GOROOT='C:\path\to\go1.23.12'
$env:PATH="$env:GOROOT\bin;$env:PATH"
go test ./...
go build -trimpath -o bin/manager.exe ./cmd/manager
helm lint charts/backup-restore-operator
kubectl kustomize config/default > deploy/install.yaml
```

安装 Kustomize 默认清单需要 cert-manager：

```powershell
kubectl apply -k config/default
```

Helm 安装：

```powershell
helm upgrade --install backup-restore charts/backup-restore-operator `
  --namespace backup-system --create-namespace `
  --set clusterRef=cluster-a `
  --set image.repository=registry.example/backup-restore-operator `
  --set image.tag=0.1.0
```

示例在 [`config/samples`](./config/samples)。详细实现、部署和测试证据见 [`docs/backup-restore`](./docs/backup-restore)。

Docker Desktop 本地部署使用 [`deploy/docker-desktop-values.yaml`](./deploy/docker-desktop-values.yaml)，本次实际安装、验证对象与清理方法见 [`deploy/DOCKER-DESKTOP.md`](./deploy/DOCKER-DESKTOP.md)。

本地管理员 Web UI 已随 Helm Chart 部署。执行 `.\deploy\start-webui.ps1` 后访问 <http://localhost:8082>；页面使用说明、安全边界和管理 API 见 [`deploy/WEBUI.md`](./deploy/WEBUI.md)。

## 生产边界

- V1.0 只承诺同集群 CSI 快照恢复，不承诺快照跨集群/跨存储可移植。
- Local 仓库必须预挂载到 Manager Pod，并通过 `nodeSelector` 固定节点；当前版本每个 Operator 实例支持一个预挂载 Local 根目录。生产灾备优先使用 SFTP。
- 工作负载执行当前在分阶段 Reconcile 内完成；超大集群建议在下一阶段迁移到隔离 Worker Job。
- Hook 字段和执行接口已预留，但 V1.0 Webhook 明确拒绝非空 Hook；文件级卷备份、S3/OBS/MinIO、跨集群恢复不在本版本。
- Go module 仍为 `github.com/example/backup-restore-operator`，接入企业仓库前必须整体替换。
