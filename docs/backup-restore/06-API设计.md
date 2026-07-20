# 容器平台备份与恢复插件：REST API 草案

> 本文件是未来管理员控制台的 REST API 草案，不表示当前仓库已经包含独立 API Server。当前发行物是单集群管理员/单租户 Operator，集群管理员直接通过 Kubernetes API/CRD 操作；普通用户不得直接访问 Cluster-scoped CRD。若未来向普通用户开放，必须新增平台外部 ACL/API，并在每次预览、备份和恢复执行时复验 Namespace 权限；`includeNamespaces` 不能作为授权依据。

## 1. API 约定

### 1.1 基础规范

- Base path：`/api/v1/backup-restore`；HTTPS only；JSON，UTF-8。
- 当前仅集群管理员可调用本草案 API；普通用户不直接 get/list/watch 或写 Cluster-scoped CRD。
- 管理员 API 可沿用平台 Bearer/OIDC；动作采用 `backup.repository.manage`、`backup.policy.manage`、`backup.execute`、`backup.record.delete`、`restore.execute` 等。未来普通用户接入还必须增加独立的对象与 Namespace ACL，不得仅复用这些动作名。
- 创建/动作请求支持 `Idempotency-Key`（1–128 字符）；服务端保存 subject+path+bodyHash 24h。同 key 同 body 返回原结果，不同 body 返回 409。
- 列表：`pageSize` 默认 20、最大 200；`continue` 为不透明游标；稳定排序默认 `creationTime desc,name asc`。
- 乐观并发：更新携带 `resourceVersion`；过期返回 409。异步操作返回 `202 Accepted` 与 `operation/task` 链接。
- 删除高危资产不使用裸 `DELETE` 传复杂语义，而使用 `:delete` action；随后由服务端提交 Kubernetes DELETE。
- 时间 RFC3339；duration 字符串；quantity 字符串；字段未设置省略而非 `null`。

### 1.2 通用请求上下文

查询参数：`clusterRef` 必填，并且当前必须是该 Operator 管理的单集群。`clusterRef` 用于路由和同集群引用校验，不是终端用户授权字段。

通用响应：

```json
{
  "apiVersion": "backup-restore.platform.io/v1",
  "requestId": "req-7d9e",
  "data": {},
  "warnings": []
}
```

通用错误：

```json
{
  "apiVersion": "backup-restore.platform.io/v1",
  "requestId": "req-7d9e",
  "error": {
    "httpStatus": 422,
    "code": "BR-SELECTION-INVALID-001",
    "message": "备份范围没有匹配到可备份资源",
    "retryable": false,
    "field": "spec.selection.includeNamespaces",
    "details": [{"namespace": "app-a", "reason": "NotFound"}],
    "help": "请选择当前集群中存在且需要保护的命名空间"
  }
}
```

HTTP：400 语法、401 未认证、403 未授权（不泄漏对象是否存在）、404 授权范围内不存在、409 冲突/资源版本/幂等 key、422 业务校验、429 限流、500 内部、502/503 外部依赖、504 超时。

## 2. Repository API

| Method/Path | 参数/请求体 | 返回 | 权限 | 典型错误 |
|---|---|---|---|---|
| GET `/repositories` | cluster,type,phase,enabled,keyword,pageSize,continue | Repo 脱敏摘要列表 | repository.read | 403、BR-CLUSTER-NOTFOUND-002 |
| POST `/repositories` | BackupRepository spec（无 status） | 201 Repo Pending | repository.manage | BR-REPO-INVALID-004、BR-SECRET-REF-001 |
| GET `/repositories/{name}` | include=conditions,relations,events | 脱敏详情 | repository.read + scope | 404 |
| PUT `/repositories/{name}` | resourceVersion + 可变字段 | 200 新 generation | repository.manage | 409、BR-IMMUTABLE-FIELD-001 |
| POST `/repositories/{name}:check` | `{"mode":"ReadWrite","timeout":"30s"}` | 202 operation；或 `wait=true` 最大 30s 返回分步结果 | repository.check | BR-REPO-CONNECT-001 等 |
| POST `/repositories:check` | draft Repo config | 不落 CR 的测试结果；SecretRef 必须已存在 | repository.manage | 同上 |
| POST `/repositories/{name}:enable|disable` | resourceVersion,reason | 200 Repo | repository.manage | 409/引用状态 |
| POST `/repositories/{name}:delete` | `mode=Safe|OrphanRecords`,confirmName,confirmationToken | 202 deletion | repository.delete；Orphan 需 platform.admin | BR-REPO-INUSE-005 |

创建 SFTP 请求示例：

```json
{
  "metadata": {"name": "sftp-prod", "displayName": "生产 SFTP"},
  "spec": {
    "clusterRef": "cluster-a", "type": "SFTP", "enabled": true,
    "sftp": {
      "host": "sftp.internal", "port": 22, "basePath": "/backup/cluster-a",
      "auth": {
        "type": "PrivateKey",
        "usernameRef": {"namespace": "backup-system", "name": "sftp-prod", "key": "username"},
        "privateKeyRef": {"namespace": "backup-system", "name": "sftp-prod", "key": "id_ed25519"}
      },
      "knownHostsRef": {"namespace": "backup-system", "name": "sftp-prod", "key": "known_hosts"},
      "maxConnections": 4
    },
    "compression": {"algorithm": "Gzip", "level": 6},
    "encryption": {"enabled": true, "algorithm": "AES256GCM", "keyRef": {"namespace": "backup-system", "name": "backup-kek", "key": "key"}}
  }
}
```

检查响应包含 `steps[]`：`DNS,TCP,SSH_HANDSHAKE,AUTH,HOST_KEY,PATH,WRITE,RENAME,READ,DELETE,CAPACITY`，每项含 status/latency/errorCode；不包含凭据或服务端 banner 中可能的敏感信息。

## 3. Policy Selection 与 Preview API

| Method/Path | 参数/请求体 | 返回 | 权限 | 典型错误 |
|---|---|---|---|---|
| POST `/policy-previews` | 完整 Policy draft、`sampleLimit`≤100 | 202 Preview | policy.manage | BR-SELECTION-INVALID-001 |
| GET `/policy-previews/{id}` | page/GVR/namespace/filter | Preview 状态和分页结果 | preview owner/read | 404/410 expired |
| POST `/policies/{name}:preview` | optional selection overrides | 202 Preview，并刷新 `status.selectionPreview` | policy.manage | discovery/429 |
| GET `/clusters/{cluster}/api-resources` | scope=Namespaced\|Cluster,search | 规范 GVR、kind、verbs、category | policy.read | 503 discovery |
| GET `/clusters/{cluster}/snapshot-capabilities` | namespace/storageClass/driver | CSI/VSC 支持矩阵 | policy.read | snapshot API absent |

Preview 请求/响应摘要：

```json
{
  "policy": {
    "clusterRef": "cluster-a",
    "selection": {
      "mode": "Namespace",
    "includeNamespaces": ["app-prod"],
    "resources": {"includeNamespaced": ["deployments.apps", "persistentvolumeclaims"]},
    "includeClusterResources": false,
    "pvc": {"enabled": true, "snapshotTimeout": "10m", "failurePolicy": "Fail"}
    },
    "repositoryRef": {"name": "sftp-prod"}
  },
  "sampleLimit": 50
}
```

```json
{
  "data": {
    "id": "preview-01", "phase": "Complete", "hash": "sha256:...", "expiresAt": "2026-07-13T06:20:00Z",
    "summary": {"namespaces": 1, "resources": 92, "pvcs": 3, "snapshotCapablePVCs": 2, "unsupportedPVCs": 1, "estimatedBytes": 1300000},
    "issues": [{"severity": "Blocking", "code": "BR-SNAPSHOT-NOTSUPPORTED-001", "objectRef": "app-prod/cache", "message": "StorageClass nfs has no CSI snapshot support"}]
  }
}
```

## 4. Policy API

| Method/Path | 请求/返回 | 权限 | 错误 |
|---|---|---|---|
| GET `/policies` | 筛选 cluster/enabled/phase/repo/selectionMode；返回分页摘要 | policy.read | 403 |
| POST `/policies:validate` | Policy draft；返回引用状态、nextRuns[5]、DST/missed warnings | policy.manage | BR-POLICY-CRON-001 |
| POST `/policies` | Policy spec；201 | policy.manage | cron/ref/retention 422 |
| GET/PUT `/policies/{name}` | 详情/乐观并发更新 | policy.read/manage | 404/409/immutable |
| POST `/policies/{name}:enable|disable|suspend|resume` | resourceVersion,reason,`skipImmediate`（resume 可选） | policy.manage | Repo not ready/Selection invalid |
| POST `/policies/{name}:run` | Idempotency-Key；202 `source.type=Policy` BackupTask；不修改调度时间 | backup.execute | ref/permission/capacity |
| POST `/policies/{name}:clone` | newName,optional refs；201 enabled=false | policy.manage | 422 |
| DELETE `/policies/{name}` | confirmName；202；响应明确历史对象不删除 | policy.delete | 409 active mutation only |

Policy 更新响应包含 `impact`：`effectiveForNewTasks=true`、`activeTasksUnaffected`、`historicalRecordsRetentionReevaluated`。

## 5. BackupTask API（Task/Cancel/Retry/Log）

| Method/Path | 请求/返回 | 权限 | 错误 |
|---|---|---|---|
| GET `/backup-tasks` | cluster,trigger,policy,phase,errorCode,start/end,paging | task.read | 403 |
| POST `/backup-tasks` | `source.type=Policy` 时必填 policyRef；`source.type=OneTime` 时必填完整 backupSpec；202 | backup.execute | ref/selection/permission/capacity |
| GET `/backup-tasks/{name}` | `include=steps,conditions,summary`；详情 | task.read | 404 |
| GET `/backup-tasks/{name}/resources` | GVR/namespace/result/paging | 对象结果索引 | task.read | 410 detail expired |
| GET `/backup-tasks/{name}/snapshots` | result/storageClass/paging | PVC 快照结果（handle 脱敏） | task.read | 403 sensitive |
| POST `/backup-tasks/{name}:cancel` | reason,resourceVersion；202 | task.cancel | BR-TASK-NOTCANCELLABLE-002 |
| POST `/backup-tasks/{name}:retry` | optional overrides, Idempotency-Key；202 new Task | task.retry | BR-TASK-NOTRETRYABLE-003 |
| GET `/backup-tasks/{name}/logs` | `follow=false,tailLines=1000,sinceTime,level,download` | text/event-stream 或分页 JSON | task.logs | 410 expired/429 |
| GET `/backup-tasks/{name}/events` | paging | 稳定 domain events | task.read | - |

从策略立即执行：

```json
{
  "metadata": {"namePrefix": "emergency-before-upgrade"},
  "spec": {
    "clusterRef": "cluster-a",
    "trigger": "Manual",
    "source": {"type": "Policy", "policyRef": {"name": "daily-backup"}}
  }
}
```

一次性备份：

```json
{
  "metadata": {"namePrefix": "emergency-before-upgrade"},
  "spec": {
    "clusterRef": "cluster-a",
    "trigger": "Manual",
    "source": {"type": "OneTime"},
    "backupSpec": {
      "repositoryRef": {"name": "sftp-primary"},
      "selection": {"mode": "Namespace", "includeNamespaces": ["project-a"]},
      "retention": {"maxCopies": 1, "minCopies": 1, "maxAgeDays": 30},
      "timeout": "4h",
      "retryPolicy": {"maxAttempts": 3, "backoff": "30s", "maxBackoff": "10m"},
      "failurePolicy": "Continue",
      "allowPartialRecord": true
    }
  }
}
```

取消响应 `202` 仅表示已接受，最终以 Task phase 为准。过 commit point 返回 409 + `BR-TASK-NOTCANCELLABLE-002`，并说明副本完成后可删除 Record。Retry 创建新 Task，响应含 `parentTaskRef`，旧 Task 不变。

日志流事件：`{"time","level","step","message","errorCode","objectRef","traceId"}`；Secret 名可见但 `.data`、snapshotHandle、凭据和命令输出敏感段脱敏。

## 6. Record 与 Check/Delete API

| Method/Path | 请求/返回 | 权限 | 错误 |
|---|---|---|---|
| GET `/backup-records` | cluster/repo/availability/policy/time/containsPVC/paging | record.read | 403 |
| GET `/backup-records/{name}` | 详情，存储路径为相对脱敏摘要 | record.read | 404 |
| GET `/backup-records/{name}/resources` | GVR/ns/name/hasError/paging | index 元数据，不默认返回 Secret 内容 | record.read | 403 secret |
| GET `/backup-records/{name}/snapshots` | namespace/PVC/status | 快照摘要，handle 掩码 | record.read | - |
| GET `/backup-records/{name}/restores` | paging | 恢复历史 | record.read | - |
| POST `/backup-records/{name}:verify` | mode=`Full|MetadataOnly`,timeout；202 | record.verify | BR-RECORD-BUSY-001 |
| POST `/backup-records/{name}:protect` | protectedUntil/reason/ticket | 200 protection override | record.protect | 422 duration |
| POST `/backup-records/{name}:delete-preview` | mode | 引用、包/快照数、风险、confirmation nonce | record.delete | active restore conflict |
| POST `/backup-records/{name}:delete` | mode,confirmName,nonce,confirmationToken | 202 | record.delete；DataAndSnapshots 额外权限 | BR-RECORD-DELETE-... |

删除请求：

```json
{
  "mode": "DataAndSnapshots",
  "confirmName": "br-01k0-7c9d",
  "previewNonce": "delprev-9182",
  "confirmationToken": "signed-short-lived-token",
  "reason": "retention exception approved by CHG-1234"
}
```

`CROnly` 的响应必须包含 `orphanedPackage=true` 和后果；`Data` 不删快照；`DataAndSnapshots` 只删除 Record 所有权确认的快照，外部/共享快照只解除引用并警告。

## 7. Restore/Plan/Precheck API

| Method/Path | 请求/返回 | 权限 | 错误 |
|---|---|---|---|
| POST `/restore-plans` | Record UID、target、mapping、selection、PVC、conflict | 202 plan | restore.plan | Record/permission/format errors |
| GET `/restore-plans/{id}` | phase、summary、issues、diff、planHash、expiresAt | plan owner/read | 410 expired |
| GET `/restore-plans/{id}/conflicts` | type/GVR/ns/action/paging | 冲突详情 | restore.plan | - |
| POST `/restore-prechecks/target` | recordRef,targetClusterRef | 目标版本/同集群结果 | restore.plan | BR-RESTORE-CROSSCLUSTER-004 |
| POST `/restore-prechecks/namespaces` | mapping | Namespace 存在性/配额/映射结果；当前由管理员确认 | restore.plan | mapping conflict |
| POST `/restore-prechecks/volumes` | PVC/SC mapping | CSI/handle/SC/容量结果 | restore.plan | BR-RESTORE-PVC-002 |
| POST `/restore-prechecks/conflicts` | plan inputs/policies | action 可行性 | restore.plan | BR-RESTORE-CONFLICT-001 |
| POST `/restore-tasks` | 完整 spec + planHash + confirmation | 202 RestoreTask | restore.execute | stale plan/403/record state |
| GET `/restore-tasks` | cluster/record/phase/ns/time/paging | 列表 | restore.read | 403 |
| GET `/restore-tasks/{name}` | 详情/步骤/汇总 | restore.read | 404 |
| GET `/restore-tasks/{name}/objects` | action/result/GVR/ns/paging | 对象执行结果 | restore.read | - |
| GET `/restore-tasks/{name}/logs` | 同 Backup logs | SSE/JSON | restore.logs | 410 |
| POST `/restore-tasks/{name}:cancel` | reason/resourceVersion | 202 | restore.cancel | not cancellable |
| POST `/restore-tasks/{name}:retry` | optional overrides, confirmation, Idempotency-Key；先生成新 plan，若无 Blocking 则 202 新 RestoreTask | restore.execute | not retryable/plan stale/confirmation required |
| POST `/restore-tasks/{name}:clone` | 返回带原设置的新 plan draft，不直接创建 Task | restore.execute | Record no longer usable |

Restore plan 请求核心：

```json
{
  "backupRecordRef": {"name": "br-01k0-7c9d", "uid": "cb45..."},
  "targetClusterRef": "cluster-a",
  "mode": "NewNamespace",
  "namespaceMapping": {"app-prod": "app-prod-dr"},
  "resourceSelection": {"include": ["deployments.apps", "services", "configmaps", "persistentvolumeclaims"]},
  "restorePVC": true,
  "storageClassMapping": {"fast-csi": "fast-csi"},
  "conflictPolicy": {"default": "Skip", "perResource": {"configmaps": "Overwrite"}, "allowRecreate": false},
  "dryRun": false
}
```

计划 issue：`severity=Blocking|Warning|Info`、code、objectRef、source/target diff 摘要、suggestedAction。响应不返回 Secret value。提交 RestoreTask 必须原样携带 `planId/planHash`，服务端再次核验 Record checksum、cluster UID、discovery hash 和 TTL。`:retry` 不是复活旧任务：它复用选择参数、重新预检并创建 `trigger=Retry,parentTaskRef=<old>` 的新任务；若预检出现 Blocking，返回 409 和新 planId 供向导处理。未来外部 ACL 上线后，还必须在提交和 Controller 执行时核验当前授权版本。

## 8. Config、Overview 与审计 API

| Method/Path | 说明 | 权限 |
|---|---|---|
| GET `/plugin-config/cluster` | 生效配置、pending generation、系统默认 | config.read |
| PUT `/plugin-config/cluster` | resourceVersion+spec | config.manage |
| POST `/plugin-config/cluster:validate` | draft 校验，无写入 | config.manage |
| POST `/plugin-config/cluster:test-notification` | channelRef/test payload | config.manage |
| GET `/overview` | timeRange,cluster；KPI/趋势/风险 | overview.read |
| GET `/alerts` | severity/type/object/time/paging | alert.read |
| GET `/audit-events` | actor/action/object/result/time/requestId/paging | audit.read |

审计记录至少包含 actor/subject、delegated service account、action、object name/UID、cluster、before/after 字段摘要（SecretRef 仅元数据）、result/errorCode、requestId/traceId、source IP、time。审计不随业务 CR 删除。

## 9. Watch/事件推送

管理员 API 可提供 `GET /events/stream?resourceType=BackupTask&resourceName=...` SSE。当前仅向管理员会话转换内部 watch，不向普通用户透传全局 CRD watch。客户端必须处理 `event: reset` 后重新 GET；`Last-Event-ID` 最多保留 10 分钟。无 SSE 时使用 ETag/`resourceVersion` 条件轮询。未来面向普通用户时，SSE 的每个事件也必须经过外部 ACL 过滤。

## 10. 统一错误码

| 错误码 | 错误名称/触发场景 | 用户提示 | 管理员排查 | 可重试 |
|---|---|---|---|---|
| BR-REPO-CONNECT-001 | RepoConnectFailed：DNS/TCP/SSH 连接失败 | 无法连接备份仓库，请检查地址或稍后重试 | DNS、NetworkPolicy、防火墙、端口、timeout | 是 |
| BR-REPO-AUTH-002 | RepoAuthenticationFailed：密码/私钥/口令错误 | 仓库认证失败，请更新凭据 | Secret key/RV、用户名、私钥格式、服务端 auth log | 否，更新后可重试 |
| BR-REPO-HOSTKEY-003 | HostKeyMismatch | SFTP 主机身份校验失败，已阻止连接 | 比对管理员确认的指纹，更新 known_hosts；排查中间人 | 否 |
| BR-REPO-INVALID-004 | RepositoryConfigInvalid | 仓库配置不合法 | 查看 field/details，检查 path/mode/secret ref | 否 |
| BR-REPO-INUSE-005 | RepositoryInUse | 仓库仍被策略或记录引用，不能删除 | 查询 relations，先停用策略/迁移或审批 orphan | 否 |
| BR-REPO-CAPACITY-006 | RepositoryCapacityInsufficient | 仓库可用空间不足，备份未启动 | statfs/statvfs、quota、reserve、清理过期记录 | 清理后是 |
| BR-REPO-PERMISSION-007 | RepositoryPathPermissionDenied | 仓库目录不可读写 | UID/GID、目录 mode、SFTP ACL、PVC mount | 修复后是 |
| BR-SECRET-REF-001 | SecretReferenceInvalid | 凭据引用不存在或不允许 | namespace 白名单、Secret/key、RBAC、resourceVersion | 修复后是 |
| BR-SELECTION-INVALID-001 | SelectionInvalidOrEmpty | 策略选择范围无效或未匹配资源 | `spec.selection` include/exclude、GVR discovery、Namespace 是否存在 | 否 |
| BR-SELECTION-DISCOVERY-002 | SelectionDiscoveryFailed | 策略选择预览不完整 | API 429/timeout/GVR permission，刷新 Policy 预览后重试 | 是 |
| BR-POLICY-CRON-001 | InvalidCronOrTimeZone | Cron 或时区无效 | 5 字段、IANA tzdb、禁止 TZ= 前缀 | 否 |
| BR-POLICY-DUPLICATE-002 | DuplicateScheduledRun | 该调度时刻已有任务 | 核验 policyUID/scheduledTime index；通常无需处理 | 否/视为成功 |
| BR-TASK-NOTCANCELLABLE-002 | TaskNotCancellable | 任务已过可取消点或已终止 | 查看 phase/commit marker；需要时删除 Record | 否 |
| BR-TASK-NOTRETRYABLE-003 | TaskNotRetryable | 错误类型不可重试 | 查看根因；修复 spec/权限/数据后新建 Task | 否 |
| BR-SNAPSHOT-NOTSUPPORTED-001 | SnapshotNotSupported | PVC 的存储类型不支持 CSI 快照 | Snapshot CRD/controller、CSIDriver、VSC driver 匹配 | 修复能力后是 |
| BR-SNAPSHOT-TIMEOUT-002 | SnapshotTimeout | PVC 快照未在规定时间就绪 | VS/VSC events、CSI sidecar/driver、后端存储 | 是 |
| BR-SNAPSHOT-PARTIAL-003 | SnapshotPartiallyFailed | 部分 PVC 未形成快照 | 按失败 PVC/driver 排查，决定仅元数据或重试 | 是 |
| BR-BACKUP-UPLOAD-001 | BackupUploadFailed | 备份包上传失败，未生成可用副本 | SFTP 连接/空间/权限/staging、worker log | 是 |
| BR-BACKUP-CHECKSUM-002 | BackupChecksumMismatch | 备份完整性校验失败，副本不可恢复 | 传输/磁盘损坏、manifest、加密 key/version；保留证据 | 否 |
| BR-BACKUP-PACKAGE-003 | BackupPackageInvalid | 备份包生成或格式不合法 | workspace 空间、路径逃逸保护、serializer | 依原因 |
| BR-RECORD-BUSY-001 | RecordBusy | 副本正在校验、恢复或删除 | 等待当前 action，检查卡住操作 | 是 |
| BR-RECORD-BROKEN-002 | RecordBroken | 副本已损坏，禁止恢复 | 完整校验详情；从其他副本恢复 | 否 |
| BR-RECORD-SNAPSHOT-003 | RecordSnapshotMissing | 副本的一个或多个快照丢失 | VS/VSC/后端 snapshot、生命周期/外部删除 | 否；可仅元数据 |
| BR-RESTORE-CONFLICT-001 | RestoreResourceConflict | 目标资源已存在且策略不允许处理 | 查看 diff，选择 Skip/允许的 Overwrite/Rename | 修改计划后是 |
| BR-RESTORE-PVC-002 | RestorePVCFailed | PVC 无法从快照恢复 | handle、driver、SC、quota、binding mode、events | 依原因 |
| BR-RESTORE-PLAN-STALE-003 | RestorePlanStale | 恢复计划已过期或环境已变化 | 重新预检查；核对 Record/discovery/authz hash | 是 |
| BR-RESTORE-CROSSCLUSTER-004 | CrossClusterNotSupported | V1.0 不支持跨集群快照恢复 | 使用同集群目标；规划数据搬运能力 | 否 |
| BR-RESTORE-WEBHOOK-005 | RestoreWebhookRejected | 目标集群 Webhook 拒绝资源 | webhook log/policy、dry-run、证书/Service | 修复后是 |
| BR-RESTORE-IMMUTABLE-006 | ImmutableFieldConflict | 目标对象不可变字段与副本不同 | 选择 Skip；管理员评估受控重建；PVC 禁止自动重建 | 否 |
| BR-PERMISSION-DENIED-001 | PermissionDenied | 无权访问或执行该操作 | 当前检查 Kubernetes RBAC 与 ServiceAccount 模拟风险；未来再检查平台外部 ACL/API 审计 | 授权后是 |
| BR-IMMUTABLE-FIELD-001 | ImmutableFieldModified | 创建后不可修改该字段 | 新建对象并迁移 Policy 引用 | 否 |
| BR-DEPENDENCY-UNAVAILABLE-001 | DependencyUnavailable | 依赖服务暂不可用 | API Server/CSI/Webhook/Repo/目录服务健康 | 是 |
| BR-INTERNAL-001 | InternalError | 系统内部错误，请提供请求 ID | 以 trace/request ID 查结构化日志和 controller panic | 是 |

错误码稳定性：已发布 code 不改变语义；更细分错误新增 code，旧客户端仍可依 `retryable` 与 HTTP status。用户 message 可本地化，日志以 code/reason 为准。
