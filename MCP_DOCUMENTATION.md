# Data Fabric MCP 使用说明

Data Fabric 支持 MCP stdio 和远程 Streamable HTTP 两种传输。两种传输共用数据源配置和统一查询服务。远程请求始终携带 Authorization: Bearer JWT，但应由 mcp.auth_mode 决定验证方式：local-jwt 仅用于开发，release 模式的远程 MCP 必须使用 OIDC。完整的自托管首次使用路径见[部署指南](docs/mcp-deployment-zh.md)。

## 前置条件

- 从源码运行需要 Go 1.26.1 或更高版本。
- 已配置并可访问的元数据数据库，以及至少一个已登记的数据源。
- Python 3 和 requirements-sql-translation.txt 中固定的 SQLGlot 依赖。默认运行时使用 SQLGlot 做只读 SQL 分析与方言转换；Docker 镜像已包含 Python 和该依赖。
- 从仓库根目录启动程序，以便定位 scripts/sql_translate.py。

## 启用和启动

在 configs/config.yaml 中设置：

~~~yaml
mcp:
  enabled: true
  transport: "stdio"
~~~

然后运行：

~~~bash
go run ./cmd/server
~~~

mcp.enabled 默认为 false，因此普通 HTTP 服务仍是默认启动模式。MCP stdio 模式会占用进程标准输入和输出，应由 MCP 客户端启动并管理该进程，不要把输出重定向到协议客户端的标准输出。

## 远程 Streamable HTTP

端点默认监听 127.0.0.1:8081，路径为 /mcp。可配置 tls_cert_file 和 tls_key_file 由服务直接提供 TLS；否则必须绑定 loopback，并放在 HTTPS 反向代理之后。未启用直接 TLS 时，启动校验会拒绝非 loopback 监听地址。

每个远程请求都必须携带 Authorization 请求头中的 Bearer token。mcp.auth_mode 决定令牌校验方式：

- local-jwt 使用 SECURITY_JWT_SECRET 校验本地 HS256 JWT，仅用于开发和本地集成。
- oidc 使用配置的 HTTPS issuer discovery 与 JWKS 校验身份提供方签发的 access token，并检查 issuer、audience、有效期和必需 scope。release 模式下远程 MCP 必须配置为 oidc。

生产配置至少要设置 mcp.auth_mode: oidc、mcp.oidc_issuer、mcp.oidc_audience 和 mcp.oidc_required_scopes。默认 scope 为 data-fabric.query；默认 claim 映射为 sub（稳定 principal）、tenant、roles 和 preferred_username。可按身份提供方实际发出的 claim 名调整 mcp.oidc_scope_claim、mcp.oidc_tenant_claim、mcp.oidc_roles_claim 和 mcp.oidc_username_claim。issuer 必须为 HTTPS；audience 和 scope 必须与身份提供方针对 Data Fabric 资源签发的 access token 一致。

REST 管理 API 的短时 HS256 JWT 与远程 MCP 的 OIDC access token 是不同凭据。令牌的 sub 应与登记数据源时使用的 principal 对齐，tenant claim 应与数据源租户一致；不要将管理 JWT 交给 MCP，也不要把 SECURITY_JWT_SECRET 当作 OIDC 签名密钥。网关只验证 OIDC token，不签发 token，也不提供 OAuth 授权服务器或动态客户端注册。

release 模式还要求管理认证、数据库只读会话和审计保持开启。自托管远程部署、客户端配置、principal/tenant 对齐和首次查询步骤见[部署指南](docs/mcp-deployment-zh.md)。

## 可用工具

- list_data_sources：列出数据源 ID、名称、类型、状态和连接位置摘要。
- get_data_source_info：返回指定数据源的基本信息和连接位置摘要，不返回用户名或密码。
- execute_sql_query：通过统一查询服务执行查询；可选 source_dialect 用于在执行前转换 SQL；可选 parameters 为有序绑定值数组（null/布尔/数字/字符串，最多 64 个），由驱动绑定到 `?` 或 `$1..$N` 占位符，绝不拼接进 SQL 文本，续批与计数复用同一组绑定值。MCP 与 REST 会保留接收到的 JSON 中签名 64 位整数的精确数字；客户端也需要精确序列化整数，例如 JavaScript Number 无法精确表示 ±(2^53 − 1) 之外的所有整数。字符串参数仍按字符串绑定。
- fetch_next_batch：使用 execute_sql_query 返回的 query_id、slug 和 token 获取下一批结果。
- validate_sql_query：对查询请求进行校验；可选 source_dialect。
- list_tables：按数据源类型查询表名。关闭表策略时，PostgreSQL 使用数据源配置的 schema；未配置时使用 `public`。开启表策略时按已有元数据授权规则过滤。若不同 schema 有同名表，在 describe_table 中显式指定 `schema`。
- describe_table：返回指定表的列名、数据库类型、可空性、主键和索引（#184）。先用 list_tables 枚举表，再用 describe_table 检查单表结构后再编写 SQL；驱动不支持元数据发现时明确报错，不会猜测表结构。
- translate_sql：在支持的 SQL 方言之间转换 SQL。
- list_supported_dialects：列出转换器声明支持的方言。

方言转换目前覆盖 MySQL、PostgreSQL、Trino、Spark、Oracle、SQL Server、Snowflake、SQLite、DuckDB 和 Redshift，并将部分兼容数据库名称映射到相应方言。实际转换能力取决于语句和目标数据库的语法；转换成功不等同于语义等价。

## 安全边界

MCP stdio 进程具有启动它的 MCP 客户端进程权限，适合本地可信客户端。远程 Streamable HTTP 始终要求有效 Bearer JWT，并在每次请求上重新验证令牌；工具执行使用 JWT 身份与现有数据源所有者、租户和管理员授权规则。远程部署必须通过 HTTPS 保护客户端到服务端的链路，可由 Data Fabric 直接终止 TLS，也可由绑定在 loopback 的服务置于 TLS 反向代理后。

数据源信息工具不返回用户名和密码。查询仍受只读校验和数据库账号权限约束。生产 Streamable HTTP 使用 OIDC access token 验证；本地 local-jwt 模式只适合开发。Data Fabric 不签发 OAuth token，也不提供动态客户端注册功能。
