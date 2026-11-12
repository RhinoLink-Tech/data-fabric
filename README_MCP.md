# Data Fabric MCP

Data Fabric 提供可选的 MCP stdio 与远程 Streamable HTTP 服务，让 AI 客户端通过统一查询服务访问已配置的数据源。远程请求使用 `Authorization: Bearer`；release 模式校验 OIDC access token，本地 HS256 JWT 仅用于开发，并执行数据源所有者与租户授权。

远程传输默认只监听 loopback；对外部署需通过 HTTPS 反向代理，或配置服务端 TLS 证书。配置、工具说明和安全边界以 [MCP_SERVER.md](MCP_SERVER.md) 为准；[MCP 使用说明](MCP_DOCUMENTATION.md)保留本地与开发流程，[MCP 架构](MCP_ARCHITECTURE.md)展示请求路径。自托管远程部署步骤见[中文部署指南](docs/mcp-deployment-zh.md)和[英文部署指南](docs/mcp-deployment.md)。

MCP 默认关闭。HTTP 与 MCP 服务启动前均需要通过 DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID 和 DATA_FABRIC_CREDENTIAL_KEYS 注入数据源凭据加密密钥。密钥生成和轮换说明见配置指南。启用方法：

~~~yaml
mcp:
  enabled: true
  transport: "stdio"
~~~

然后从仓库根目录运行：

~~~bash
go run ./cmd/server
~~~
