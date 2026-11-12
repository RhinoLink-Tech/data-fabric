# Data Fabric 自托管 MCP 部署指南

英文完整指南：[Self-Hosted Remote MCP Deployment](mcp-deployment.md)。本文覆盖当前 MySQL 元数据库 + 远程 MCP 部署路径。

这是当前的合成数据首次使用 profile，画像与边界见[首个采用者说明](product/first-adopter-profile.md)。该画像仍是假设；尚无外部团队评估或生产结论。

## 当前已发布的 Preview

`v2.1.0-preview.1` 已于 2026-11-11 发布，GHCR 镜像包现已公开。发布镜像工作流和精确 digest 的 clean-runner 验收均已通过。无需本地编译的安装方式见下方“使用发布镜像”步骤；请从[对应 GitHub Release](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1)复制完整 tag 与 digest。本版本范围是一台 `linux/amd64` 主机，不承诺 HA、自动故障切换、响应时间 SLA 或从 v2.0.0 原地升级。外部首次使用仍由 [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442) 和 [#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446) 跟踪。

## 当前部署拓扑

Compose 会启动四个服务：MySQL 元数据库、只读示例 MySQL、HTTP 管理 API、启用 OIDC 的远程 MCP。管理 API 仅绑定本机回环地址；MCP 通过 TLS 暴露。服务使用非 root 用户。当前 profile 是单机部署，不提供 HA/自动故障切换，也不会代替你运行 OIDC 身份提供方。

## 可选：接入本机 DataSemanticHub 做容器级验收

默认部署 profile 自带元数据库和样例库。要在本机 Docker 网络中用现有
MySQL/MariaDB 做容器级验证，可使用
[外部 MySQL Compose profile](../deploy/mcp/docker-compose.external-mysql.yml)
和 WSL 验收脚本：

```bash
cd /home/fengfei/data-fabric
scripts/mcp/dsh-local-acceptance.sh \
  --image 'ghcr.io/rhinolink-tech/data-fabric@sha256:<64 位十六进制 digest>'
```

脚本默认使用 `/home/fengfei/DataSemanticHub/deploy`、
`datasemantichub-mariadb` 和 `datasemantichub-network`；可通过
`--deploy-dir`、`--db-container`、`--network` 覆盖。它会检查 deploy
目录中声明了 MariaDB 服务，仅在原容器处于停止状态时启动它，并让一个
Data Fabric API 容器加入现有网络。API 默认只绑定本机 loopback 的
18180 端口；端口冲突时可传 `--api-port`。

验收镜像必须已存在于本地 Docker 引擎；脚本不会拉取镜像。运行时会创建
带随机名称的合成元数据库和样例源库，样例源账号只有 `SELECT`、
`SHOW VIEW` 权限；随后验证 API 健康状态、未认证请求拒绝、数据源注册，
并通过 Docker 网络查询两条合成记录。收尾会删除临时 API 项目、schema、
账号和本地密钥文件，并将 MariaDB 恢复到原先的运行/停止状态。不会读取或
使用 DataSemanticHub 中已有的 schema 和数据。

此本地 profile 覆盖 release 镜像的 REST 与 MySQL 运行链路，但不覆盖
OIDC/MCP、双副本或独立操作者演练，也不能替代
[#446 的独立操作者演练](https://github.com/RhinoLink-Tech/data-fabric/issues/446)。
发布镜像工作流、GHCR 匿名拉取和精确 digest 的 clean-runner 验收已通过；这些结果仍不能替代 #446 的两名独立操作者演练。

## 前提

- Docker Engine 24+、Docker Compose plugin 2.24+
- Python 3（仅使用标准库）、OpenSSL
- 一个 HTTPS OIDC identity provider，能够签发 JWT access token，并配置 Data Fabric MCP audience 和 `data-fabric.query` scope
- 在仓库根目录执行下列命令

## 1. 初始化本地配置

```bash
python3 scripts/mcp/onboard.py init
```

命令会创建 `deploy/mcp/.env`，若从 Git 检出运行，则记录当前源码 SHA；源码压缩包会标记为 `unknown`，不能用于可验证的候选备份。命令还会生成管理 API 密钥、数据库密码、32 字节 credential-vault key 和本地 TLS 证书。本地 Compose 构建会把该 revision 写入镜像 OCI label，供恢复清单绑定源码和镜像身份。`.env` 以 `0600` 权限原子写入。初始化不会替换非空密钥、已有数据库卷或已有 TLS 文件；再次运行只填补空值。若 keyring 状态不明确或证书/私钥只存在一份，会停止并保留现有文件。

在 `deploy/mcp/.env` 设置：

- `MCP_OIDC_ISSUER`：provider 的 HTTPS issuer URL
- `MCP_OIDC_AUDIENCE`：为 Data Fabric 注册的 resource audience
- `MCP_OIDC_REQUIRED_SCOPES`：至少包含 `data-fabric.query`
- 若 provider 的 claim 名不同，再设置 `MCP_OIDC_TENANT_CLAIM`、`MCP_OIDC_ROLES_CLAIM`、`MCP_OIDC_USERNAME_CLAIM`

初始化生成的自签名 TLS 证书仅用于 `localhost`、`data-fabric.local` 和 `127.0.0.1` 私有试用。互联网部署应替换为域名证书。MCP 私钥通过 `DATA_FABRIC_TLS_GID` 指定的数字 supplementary group 交给非 root 容器读取，权限为 `0640`；不要把私钥改成 world-readable。私钥目录也必须允许该组进入。初始化会先校验已有 keyring 结构、active ID 和密钥长度；发现不一致时停止，不会自动替换密钥。

## 2. 启动与诊断

```bash
cd deploy/mcp
docker compose up -d --build
python3 ../../scripts/mcp/onboard.py doctor
```

开发者可继续用源码构建。tag workflow 成功且匹配的 GitHub Release 记录了公开 digest 后，再将完整 `tag@sha256` 镜像引用复制到 `.env` 的 `DATA_FABRIC_IMAGE`，然后用：

```bash
docker compose -f docker-compose.yml -f docker-compose.release.yml pull
docker compose -f docker-compose.yml -f docker-compose.release.yml up -d
```

该 release overlay 会移除本地 build 配置，并让 API 与 MCP 使用同一个 tag@sha256 镜像。运行主机不需要 Go、源码编译或手动安装 Python 第三方包；初始化与诊断脚本只用 Python 标准库。镜像还包含 SQLGlot 运行时、项目与第三方 notices，以及按实际 Go 模块版本生成的许可证材料和 SHA-256 manifest。**已发布 Preview：** `v2.1.0-preview.1` 的公开镜像 digest 为 `sha256:7d47073fe806879968ed3c66f9d3b2b7b7a2020f419738c2cbb1b89d58f1ebbf`。对应的 [GitHub Release](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1)记录了精确镜像引用和验收结果；公开发布及匿名 clean-runner 镜像验收已完成。issue #442 仍开放跟踪剩余首次采用者证据。

`doctor` 检查 `.env` 和 keyring、Compose 配置、四个服务状态、管理 API health、MCP TLS/未认证 `401` 以及镜像中的 SQLGlot parser；不遍历或连接所有已注册数据源。它不会启动、重建或停止服务。

下面的可选命令会向 OIDC discovery URL 和 JWKS endpoint 分别发送 HTTPS GET；受限网络环境下不要添加 `--probe-oidc`。执行前先设置身份与租户变量：

```bash
export DF_USER_ID='<provider JWT 中稳定的 sub>'
export DF_TENANT_ID='quickstart'
export MCP_ACCESS_TOKEN='<provider-issued JWT access token>'
python3 ../../scripts/mcp/onboard.py doctor --probe-oidc \
  --api-user "$DF_USER_ID" --tenant "$DF_TENANT_ID"
```

`--api-user` 检查 MCP token 中未验证的 `sub`、tenant、issuer、audience、scope 和时间字段是否与管理 principal/profile 对齐。它只提供配置提示，不验证签名；真正的签名、issuer、audience、有效期和 scope 验证仍由 MCP 网关逐请求执行。`$DF_USER_ID` 必须与 provider token 的稳定 `sub` 完全一致。

## 3. 生成短时管理 token

管理 API 的 HS256 token 与 MCP provider token 是两种不同凭据。以 provider access token 中的稳定 `sub` 作为管理 principal，并使用相同 tenant：

```bash
export DF_USER_ID='<provider JWT 中稳定的 sub>'
export DF_TENANT_ID='quickstart'
export DF_MANAGEMENT_TOKEN="$(python3 scripts/mcp/onboard.py token \
  --user "$DF_USER_ID" --tenant "$DF_TENANT_ID")"
```

默认有效期 15 分钟，最多 15 分钟；工具只把 token 写到标准输出，不落盘、不输出密钥。普通 quickstart 不需要 admin role。只有需要管理 API 的 admin-only 操作时才显式传 `--admin`。不要把本地 HS256 token 发送给 MCP，也不要提交到仓库、日志、shell 脚本或 issue。

将 `DF_MANAGEMENT_TOKEN` 用于注册/管理 REST 请求；将 provider 签名的 JWT 设置到 `MCP_ACCESS_TOKEN` 并交给 MCP client。注册数据源的 API principal 和 MCP token 的 `sub` 应一致；若多个 app 要共享来源，使用相同 tenant 并为每个客户端提供自己的 OIDC identity。应用权限、tenant 归属、只读策略和审计仍由网关强制执行。

生成第 3 节的短时管理 token 后，如需检查某一个额外数据源，可给 doctor 一个 JSON 配置文件（必须为 owner-only，例如 `chmod 600 source.json`），而不是扫描整个数据源列表：

```bash
export DF_MANAGEMENT_TOKEN='<短时管理 API token>'
python3 ../../scripts/mcp/onboard.py doctor \
  --source-type mysql --source-config source.json
```

该检查只把选择的连接配置发往本机 loopback 管理 API 的 connection-test 路由；不会注册数据源，也不会在输出中打印凭据。只有 API 明确返回 healthy 才报告成功；HTTP 200 但连接状态为 unhealthy 仍报告失败。失败时会给出 correlation ID，供本机日志排查。标准输出和日志不要粘贴到公共 issue 中。

## 4. 首次任务和恢复
管理页面内置了可复制的 MCP 连接命令、首次查询任务和第二应用身份说明，详见[数据源管理界面与首次授权任务](data-source-management-ui-zh.md)。

连接示例 MCP server 后，按顺序执行 `list_data_sources`、`list_tables`、`describe_table` 和只读 `execute_sql_query`。示例来源只包含合成 `orders` 数据。查询分页、排序和跨页数据变化边界见[查询结果语义](query-result-semantics-zh.md)。

`doctor` 发现初始化、keyring、OIDC、TLS、Compose 或 parser 问题时，按错误项中的配置名修复，再运行一次。不要通过重置元数据库或替换 keyring 来“修复”已有安装：换 key 可能使已保存来源凭据无法解密。当前 profile 的加密备份、校验和新项目恢复步骤见[备份与恢复操作手册](operations/mcp-profile-backup-recovery-zh.md)（[English](operations/mcp-profile-backup-recovery.md)）；版本升级边界见[升级指南](upgrading-v1.2.0.md)与 [MySQL 升级演练](operations/mysql-upgrade-rehearsal.md)。

该 profile 当前是单机、MySQL 元数据库和 MySQL sample source。它不证明外部团队采用、生产可用性或商业购买意愿；这些仍须分别验证。
