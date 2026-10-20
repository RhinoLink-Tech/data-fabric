# Data Fabric MCP 使用说明

Data Fabric 支持 MCP stdio 和远程 Streamable HTTP 两种传输。两种传输共用数据源配置和统一查询服务；远程请求必须携带标准 Authorization: Bearer JWT，并按令牌中的用户和租户权限访问数据源。

## 前置条件

- Go 1.26.1 或更高版本。
- 已配置并可访问 Data Fabric 元数据数据库。
- 如果使用 SQL 方言转换功能，需要 Python 3，并在运行环境安装 sqlglot：python3 -m pip install -r requirements-sql-translation.txt。
- 从仓库根目录启动程序，以便转换脚本能被定位。

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

远程模式使用 MCP Streamable HTTP，默认监听 `127.0.0.1:8081`，端点为 `/mcp`。每个初始化、工具调用、GET 监听和会话请求都必须通过 `Authorization: Bearer <JWT>` 认证；未认证、无效、过期或缺少 exp 的令牌会在工具执行前被拒绝。令牌使用 Data Fabric 的 HS256 JWT 验证器（`SECURITY_JWT_SECRET`），必须包含有效的 `exp`，身份以标准 JWT `sub` 为准，并兼容已有 `userId` 声明；可带 `tenantId` 和 `roles` 声明用于租户和管理员授权。令牌由部署方的可信身份服务或运维人员签发；本传输提供令牌验证，不提供 OAuth 授权服务器。

推荐让 TLS 反向代理对外提供 HTTPS，并将 Data Fabric 绑定在 loopback：

~~~yaml
mcp:
  enabled: true
  transport: "streamable-http"
  address: "127.0.0.1:8081"
~~~

若由 Data Fabric 直接提供 TLS，配置证书和私钥，并绑定到部署环境需要的地址：

~~~yaml
mcp:
  enabled: true
  transport: "streamable-http"
  address: "0.0.0.0:8081"
  tls_cert_file: "/etc/data-fabric/tls.crt"
  tls_key_file: "/etc/data-fabric/tls.key"
~~~

没有直接 TLS 证书时，程序拒绝绑定非 loopback 地址。通过反向代理部署时，客户端仍应使用 HTTPS 地址；不要把未加密的内部监听端口暴露到不可信网络。远程连接需要配置有效的 JWT 密钥；密钥不得提交到仓库。

自托管远程部署（元数据库 + 示例数据源 + TLS + 客户端接入示例）的完整快速上手见 [自托管远程 MCP 部署指南](docs/mcp-deployment.md)。

## 可用工具

- list_data_sources：列出数据源 ID、名称、类型、状态和连接位置摘要。
- get_data_source_info：返回指定数据源的基本信息和连接位置摘要，不返回用户名或密码。
- execute_sql_query：通过统一查询服务执行查询；可选 source_dialect 用于在执行前转换 SQL。
- fetch_next_batch：使用 execute_sql_query 返回的 query_id、slug 和 token 获取下一批结果。
- validate_sql_query：对查询请求进行校验；可选 source_dialect。
- list_tables：按数据源类型查询表名。
- describe_table：返回指定表的列名、数据库类型、可空性、主键和索引（#184）。先用 list_tables 枚举表，再用 describe_table 检查单表结构后再编写 SQL；驱动不支持元数据发现时明确报错，不会猜测表结构。
- translate_sql：在支持的 SQL 方言之间转换 SQL。
- list_supported_dialects：列出转换器声明支持的方言。

方言转换目前覆盖 MySQL、PostgreSQL、Trino、Spark、Oracle、SQL Server、Snowflake、SQLite、DuckDB 和 Redshift，并将部分兼容数据库名称映射到相应方言。实际转换能力取决于语句和目标数据库的语法；转换成功不等同于语义等价。

## 安全边界

MCP stdio 进程具有启动它的 MCP 客户端进程权限，适合本地可信客户端。远程 Streamable HTTP 始终要求有效 Bearer JWT，并在每次请求上重新验证令牌；工具执行使用 JWT 身份与现有数据源所有者、租户和管理员授权规则。远程部署必须通过 HTTPS 保护客户端到服务端的链路，可由 Data Fabric 直接终止 TLS，也可由绑定在 loopback 的服务置于 TLS 反向代理后。

数据源信息工具不返回用户名和密码。查询仍受只读校验和数据库账号权限约束。Streamable HTTP 使用自托管 JWT 验证，不包含 OAuth 授权服务器或动态客户端注册功能。
