# 示例使用顺序

1. 安装 Operator 与 CRD。
2. 用真实值创建 `backup-system` 中的凭据 Secret；示例中的 `REPLACE_ME` 不能用于生产。
3. 创建 `BackupRepository` 并等待 `status.phase=Ready`。
4. 创建内嵌 `selection` 的 `BackupPolicy`，等待范围预览和仓库检查完成。
5. 等待策略生成 `BackupTask`，或提交仅引用 `policyRef` 的手动任务。
6. 仅对 `Available` / `PartiallyAvailable` 且 `status.restorable=true` 的恢复点创建 `RestoreTask`。

Local 示例要求 Helm 的 `localRepositoryMount` 已挂载到 `/repository`，并通过 `nodeSelector` 固定到仓库所在节点。
