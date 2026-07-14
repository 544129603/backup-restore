# 真实集群 E2E 冒烟测试

`run.ps1` 在一次性 Kubernetes 测试集群中完成以下检查：

1. Helm Chart lint、安装和 Deployment Ready；
2. 七个 CRD 均存在且为 `Cluster` scope；
3. `BackupPluginConfig/cluster` 被控制器更新为 `Ready`；
4. 启用 Webhook 时，非法 Local Repository 被 Admission 拒绝；
5. 七类集群级 API 均可查询；
6. 默认在结束后卸载 Release 并删除 CRD。

该脚本会删除集群级测试资源，检测到已有插件 CRD 时会拒绝运行，必须使用一次性测试集群。镜像必须提前推送到集群可访问的仓库：

```powershell
./test/e2e/run.ps1 `
  -Image registry.example.com/platform/backup-restore-operator:e2e `
  -ClusterRef e2e-cluster
```

默认 Admission 路径依赖 cert-manager。未安装 cert-manager 时可传入 `-DisableWebhook`，但该模式不能作为 Admission 验收通过的证据。调试时可传入 `-Keep` 保留安装；测试后必须人工清理 Release、CRD 和 Namespace。

真实 CSI Snapshot、PVC Bound、Local 节点盘和企业 SFTP 场景需要由各存储厂商的专用测试套件继续覆盖，本冒烟脚本不伪造这些外部能力。
