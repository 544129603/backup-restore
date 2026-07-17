# 备份与恢复 Web 管理控制台

## 访问地址

当前 Docker Desktop 环境已部署 Web UI。首次使用或电脑重启后，在项目根目录执行：

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
.\deploy\start-webui.ps1
```

浏览器访问：<http://localhost:8082>

停止本地端口转发：

```powershell
.\deploy\stop-webui.ps1
```

端口转发仅监听 `127.0.0.1`。脚本在后台启动 `kubectl port-forward`，PID 保存在 `%TEMP%\backup-restore-webui`，不会向局域网暴露管理接口。

## 页面能力

| 页面 | 能力 |
|---|---|
| 概览 | 查看仓库、可恢复副本、运行中任务、异常任务及最近任务 |
| 备份仓库 | 新建 Local/SFTP 配置、编辑、连通性检查、受保护删除 |
| 备份范围 | 新建/编辑范围、查看资源与 PVC 预估、刷新预览、删除 |
| 备份策略 | 新建/编辑 Cron、立即执行、启用、停用、删除 |
| 备份任务 | 创建手动任务、查看进度和完整状态、取消、删除 |
| 备份记录 | 查看副本、完整性校验、创建恢复任务、三种删除模式 |
| 恢复任务 | 创建 DryRun/实际恢复、查看进度、取消、删除 |
| 全局配置 | 查看和编辑 BackupPluginConfig |

## 操作向导

页面右上角的“操作向导”提供统一的四步式对象管理入口：

1. 选择操作：查询、创建或修改。
2. 选择对象：仓库、范围、策略、备份任务、备份记录、恢复任务或全局配置。
3. 按表单填写查询条件或对象字段；关联对象使用下拉框，仓库类型和 PVC 配置会动态展示对应字段。
4. 查看查询结果或 JSON 变更预览，确认后提交。

查询支持名称/内容关键字、状态、类型和结果数量条件，结果可直接打开详情或进入修改向导。创建和修改在提交前校验对象名称、必填项、Cron、Namespace 范围、PVC 选项和高风险恢复策略。`BackupTask`、`BackupRecord`、`RestoreTask` 的业务规格创建后不可修改，因此向导只允许查询或创建；任务取消仍使用详情页动作。

向导默认使用结构化表单。需要填写尚未图形化的扩展字段时，可点击“高级 JSON”进入原 JSON 编辑器。新建模板会自动填写 `apiVersion`、`kind`、当前 `clusterRef=docker-desktop` 以及安全默认值。恢复模板默认使用 `dryRun: true`、`restorePVC: false`、`conflictPolicy.default: Skip`，执行实际恢复前必须显式修改。

## 安全规则

- 页面不会显示 Secret 数据，只显示 CRD 中的 Secret 引用。
- 管理 API 只展示和接受 `spec.clusterRef=docker-desktop` 的业务对象。
- `status`、UID、resourceVersion、managedFields 等服务端字段不能通过编辑器修改。
- 删除 BackupRecord 时必须选择 `RecordOnly`、`RepositoryData` 或 `RepositoryDataAndSnapshots`，并再次输入对象名称。
- Web UI 使用 Operator ServiceAccount，因此属于集群管理员界面。当前版本没有登录、用户授权和多租户 ACL，不得通过 Ingress、NodePort 或公网负载均衡器直接暴露。

## 部署与验证

Web UI 与 Operator 使用同一个镜像中的不同二进制：

- Operator：`/manager`
- Web UI：`/webui`

重新构建和升级：

```powershell
docker build --build-arg VERSION=dev-local-12 -t backup-restore-operator:dev-local-12 .

helm upgrade --install backup-restore charts/backup-restore-operator `
  --namespace backup-system --create-namespace `
  -f deploy/docker-desktop-values.yaml `
  --wait --timeout 8m

kubectl rollout status deployment/backup-restore-operator-webui `
  -n backup-system --timeout=180s
```

检查状态：

```powershell
kubectl get deployment,pod,service -n backup-system
kubectl logs deployment/backup-restore-operator-webui -n backup-system --tail=200
Invoke-RestMethod http://localhost:8082/api/health
```

## 管理 API

页面调用同源 REST API：

```text
GET    /api/health
GET    /api/overview
GET    /api/resources/{resource}?q=&phase=&type=&limit=
POST   /api/resources/{resource}
GET    /api/resources/{resource}/{name}
PUT    /api/resources/{resource}/{name}
DELETE /api/resources/{resource}/{name}
POST   /api/resources/{resource}/{name}/actions/{action}
```

`resource` 支持 `repositories`、`policies`、`backup-tasks`、`records`、`restore-tasks`、`configs`。集合查询参数 `q`、`phase`、`type` 和 `limit` 均为可选；动作包括仓库刷新、恢复点校验、策略立即执行、策略启停和任务取消。`GET /api/policy-runs/{name}` 聚合展示策略关联的执行历史与恢复点。
