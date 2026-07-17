# 双架构离线镜像

## 构建

在项目根目录执行：

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
.\deploy\build-offline-images.ps1 -Version dev-local-11
```

脚本生成以下文件：

- `backup-restore-operator-dev-local-11-linux-amd64.tar`：x86_64 Docker 镜像包。
- `backup-restore-operator-dev-local-11-linux-arm64.tar`：ARM64 Docker 镜像包。
- `backup-restore-operator-dev-local-11-multiarch.oci.tar`：包含两个平台的 OCI Image Layout 归档。
- `backup-restore-operator-dev-local-11-SHA256SUMS.txt`：SHA-256 校验文件。

## 离线导入

x86_64 主机：

```powershell
docker load -i backup-restore-operator-dev-local-11-linux-amd64.tar
docker tag backup-restore-operator:dev-local-11-amd64 backup-restore-operator:dev-local-11
```

ARM64 主机：

```powershell
docker load -i backup-restore-operator-dev-local-11-linux-arm64.tar
docker tag backup-restore-operator:dev-local-11-arm64 backup-restore-operator:dev-local-11
```

OCI 归档用于支持 OCI Image Layout 和多架构索引的镜像仓库或容器运行时。为获得最广泛的 Docker 离线兼容性，优先导入对应架构的 Docker `tar` 包。

## 校验

```powershell
Get-Content .\backup-restore-operator-dev-local-11-SHA256SUMS.txt
Get-FileHash -Algorithm SHA256 .\backup-restore-operator-dev-local-11-linux-amd64.tar
```

Helm 部署时使用统一镜像名：

```yaml
image:
  repository: backup-restore-operator
  tag: dev-local-11
  pullPolicy: IfNotPresent
```
