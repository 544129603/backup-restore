# 09 Operator 开发实现

## 1. 实现结论

本仓库已从“仅设计文档”扩展为可编译、可生成 CRD/RBAC/Webhook、可运行单元测试与 EnvTest 的 Go Operator 工程。实现沿用既有设计的 `protection.platform.io/v1alpha1`，没有另起不兼容的 API Group。

| 对象/能力 | 主要代码 | 完成状态 |
|---|---|---|
| 7 个 Cluster-scoped CRD | `api/v1alpha1`、`config/crd/bases` | 已实现 |
| 默认值、校验、不可变字段、删除确认 | `webhook_defaults.go`、`webhook_validation.go` | 已实现 |
| Repository | `repository_controller.go`、`internal/repository` | Local/SFTP 已实现 |
| Scope | `scope_controller.go`、`internal/collector`、`internal/sanitizer` | 已实现 |
| Policy | `policy_controller.go`、`internal/scheduler` | 已实现 |
| BackupTask | `backup_controller.go`、archive/checksum/encryption/snapshot | 已实现 MVP 状态机 |
| BackupRecord | `record_controller.go`、retention/lifecycle controllers | 已实现 |
| RestoreTask | `restore_controller.go`、`internal/restore` | 已实现同集群恢复 |
| Manager/观测 | `cmd/manager`、`internal/metrics` | 已实现 |
| Kustomize/Helm/CI | `config`、`charts`、`.github/workflows` | 已实现 |

## 2. 关键实现决策

### 2.1 提交与可用性屏障

备份上传采用固定协议：先上传 `resources.tar.gz`、`metadata.json`、`index.json`、`snapshots.json` 和 `sha256sum.txt`，最后写 `.done`。`BackupTask` 读回每个对象并校验后才创建 `BackupRecord`；`BackupRecordController` 再独立校验一次，之后才设置 `Available` 或 `PartiallyAvailable`。因此上传中断不会产生可用副本。

### 2.2 幂等与故障恢复

- Policy 使用 `policyUID/scheduledTimeUTC` 作为幂等键，并使用确定性 DNS 名称创建 Task。
- 快照名由 `taskUID/namespace/pvc` 哈希生成；Create 前先 Get。
- Repository Put 使用同目录临时文件，再 rename；`.done` 是最终可见屏障。
- Task 每个阶段只推进一个持久化 Phase；控制器重启后从 Phase 重入。
- 已越过 `.done` 提交点的 BackupTask 不再接受取消，而是必须完成 Record 生成，防止产生无主已提交包。

### 2.3 安全

- CR 仅保存 `namespace/name/key`，所有凭据和加密密钥从 Secret 读取，日志不输出值。
- SFTP 默认必须通过 `known_hosts` 校验；不安全模式必须显式开启。
- 资源包支持分块 AES-256-GCM，Record 冻结算法与 Secret 引用，SHA-256 校验针对最终上传字节。
- 包含 Secret 的 Scope 强制使用已启用加密的 Repository。
- `BackupRecord` 删除需要先写 `delete-mode` 与 `delete-confirmed=true`，Admission 再允许 DELETE。
- `--cluster-ref` 限定 Operator 只执行本集群对象；为空仅用于开发环境。

为支持发现/备份/恢复任意 Custom Resource，Operator ServiceAccount 需要跨 API Group 的读写权限；DELETE 只授予受控重建白名单和自身生命周期对象。该高权限账号只供 Operator 使用，普通租户不得获得 CRD list/watch 或模拟该 ServiceAccount 的权限。

### 2.4 恢复规则

恢复强制执行 Record 校验、下载校验、解密、安全解包、计划生成、冲突预检，之后按 Namespace、CRD/集群资源、PVC、其他命名空间资源执行。CRD 恢复后刷新 RESTMapper。PVC 跨 Namespace 时，基于原 snapshotHandle 创建静态 `VolumeSnapshotContent` 和目标 Namespace 中的 `VolumeSnapshot`。

Overwrite 先执行 Update；只有 `allowRecreate=true`、`highRiskConfirmed=true` 且资源类型在白名单中时，immutable 错误才触发删除重建。PVC、Namespace、CRD、Secret 不在删除重建白名单。

## 3. 与原设计的明确偏差

| 设计期设想 | 当前实现 | 原因与影响 |
|---|---|---|
| 每任务独立 Worker Job | Manager 内分阶段 Reconcile | 可运行 MVP；超大集群的资源隔离和 Pod 漂移恢复弱于 Job 模型，V1.1 应迁移 |
| 任意数量 Local Repo 动态挂载 | 每个 Operator 实例一个预挂载 Local 根目录 | Kubernetes 不能在运行中给已有 Pod 动态增加任意 PVC/hostPath；用 Helm mount + nodeSelector 明确约束 |
| BackupPluginConfig 热更新所有并发值 | Manager 启动参数控制并发，Config 负责声明/状态 | controller-runtime 并发度不能安全热改；修改后滚动重启生效 |
| 完整 server-side restore dry-run | 生成计划、查询冲突且不写资源 | MVP 可用于评审；V1.1 再逐对象调用 API Server dry-run 和 diff |

这些偏差均在部署文档和风险清单中显式展示，不作为“完整生产版”隐藏。

## 4. 目录映射

```text
api/v1alpha1/                  CRD Go 类型、默认值、Admission
cmd/manager/                   Manager 入口、leader election、健康检查
internal/controller/           9 个 Reconciler
internal/repository/           Local/SFTP Adapter 与 Secret Factory
internal/collector/            Discovery、过滤、分页采集、索引
internal/sanitizer/            status/runtime metadata/不可移植字段清洗
internal/snapshot/             CSI Snapshot 能力探测、创建、恢复引用
internal/archive|checksum/      安全归档、解包、SHA-256 manifest
internal/encryption/            分块 AES-256-GCM
internal/restore/              Plan、冲突解析、有序恢复
internal/retention/             count/age/minCopies 选择算法
config/                         CRD/RBAC/Webhook/Kustomize/sample
charts/backup-restore-operator/ Helm Chart
test/integration/              EnvTest
```

## 5. 尚未进入 V1.0 的能力

AppConsistent Hook、文件级 PVC 数据备份、S3/OBS/MinIO、跨集群恢复、跨存储数据搬迁、通用 MergePatch、Repo 复制/迁移、多集群灾备编排均未实现。Hook CRD 字段与 Executor 接口已预留，但 Admission 会拒绝非空 Hook，防止出现“字段可填但实际不执行”的假能力。
