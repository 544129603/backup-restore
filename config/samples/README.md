# 示例使用顺序

1. 安装 Operator 与 CRD。
2. 用真实值创建 `backup-system` 中的凭据 Secret；示例中的 `REPLACE_ME` 不能用于生产。
3. 创建 `BackupRepository` 并等待 `status.phase=Ready`。
4. 创建 `BackupScope` 并等待范围预览完成。
5. 创建 `BackupPolicy`，或提交手动 `BackupTask`。
6. 仅对 `Available` / `PartiallyAvailable` 且 `status.restorable=true` 的 `BackupRecord` 创建 `RestoreTask`。

Local 示例要求 Helm 的 `localRepositoryMount` 已挂载到 `/repository`，并通过 `nodeSelector` 固定到仓库所在节点。
