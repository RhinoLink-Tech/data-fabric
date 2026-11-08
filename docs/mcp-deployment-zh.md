# Data Fabric 自托管 MCP 部署指南

英文完整指南：[Self-Hosted Remote MCP Deployment](mcp-deployment.md)。本文覆盖当前 MySQL 元数据库 + 远程 MCP 部署路径。

## 当前部署拓扑

Compose 会启动四个服务：MySQL 元数据库、只读示例 MySQL、HTTP 管理 API、启用 OIDC 的远程 MCP。管理 API 仅绑定本机回环地址；MCP 通过 TLS 暴露。服务使用非 root 用户。当前 profile 是单机部署，不提供 HA/自动故障切换，也不会代替你运行 OIDC 身份提供方。

## 前提

- Docker Engine 24+、Docker Compose plugin 2.24+
- Python 3（仅使用标准库）、OpenSSL
- 一个 HTTPS OIDC identity provider，能够签发 JWT access token，并配置 Data Fabric MCP audience 和 `data-fabric.query` scope
- 在仓库根目录执行下列命令

## 1. 初始化本地配置

```bash
python3 scripts/mcp/onboard.py init
```

命令会创建 `deploy/mcp/.env`，生成管理 API 密钥、数据库密码、32 字节 credential-vault key 和本地 TLS 证书。`.env` 以 `0600` 权限原子写入。初始化不会替换非空密钥、已有数据库卷或已有 TLS 文件；再次运行只填补空值。若 keyring 状态不明确或证书/私钥只存在一份，会停止并保留现有文件。

在 `deploy/mcp/.env` 设置：

- `MCP_OIDC_ISSUER`：provider 的 HTTPS issuer URL
- `MCP_OIDC_AUDIENCE`：为 Data Fabric 注册的 resource audience
- `MCP_OIDC_REQUIRED_SCOPES`：至少包含 `data-fabric.query`
- 若 provider 的 claim 名不同，再设置 `MCP_OIDC_TENANT_CLAIM`、`MCP_OIDC_ROLES_CLAIM`、`MCP_OIDC_USERNAME_CLAIM`

初始化生成的自签名 TLS 证书仅用于 `localhost`、`data-fabric.local` 和 `127.0.0.1` 私有试用。互联网部署应替换为域名证书。MCP 私钥通过 `DATA_FABRIC_TLS_GID` 指定的数字 supplementary group 交给非 root 容器读取，权限为 `0640`；不要把私钥改成 world-readable。私钥目录也必须允许该组进入。

## 2. 启动与诊断

```bash
cd deploy/mcp
docker compose up -d --build
python3 ../../scripts/mcp/onboard.py doctor
```

本地开发者可继续用源码构建。发布镜像可用时，从 GitHub Release 复制完整的 `tag@sha256` 到 `.env` 中的 `DATA_FABRIC_IMAGE`，然后用：

```bash
docker compose -f docker-compose.yml -f docker-compose.release.yml pull
docker compose -f docker-compose.yml -f docker-compose.release.yml up -d
```

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

该检查只把选择的连接配置发往本机 loopback 管理 API 的 connection-test 路由；不会注册数据源，也不会在输出中打印凭据。失败时会给出 correlation ID，供本机日志排查。标准输出和日志不要粘贴到公共 issue 中。

## 4. 首次任务和恢复

连接示例 MCP server 后，按顺序执行 `list_data_sources`、`list_tables`、`describe_table` 和只读 `execute_sql_query`。示例来源只包含合成 `orders` 数据。

`doctor` 发现初始化、keyring、OIDC、TLS、Compose 或 parser 问题时，按错误项中的配置名修复，再运行一次。不要通过重置元数据库或替换 keyring 来“修复”已有安装：换 key 可能使已保存来源凭据无法解密。备份、恢复和升级步骤见 [升级指南](upgrading-v1.2.0.md) 与 [MySQL 恢复流程](operations/mysql-upgrade-rehearsal.md)。

该 profile 当前是单机、MySQL 元数据库和 MySQL sample source。它不证明外部团队采用、生产可用性或商业购买意愿；这些仍须分别验证。
