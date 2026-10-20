# Data Fabric MCP 架构

~~~mermaid
flowchart LR
    LocalClient[本地 MCP 客户端] <-->|stdio| Server[Data Fabric MCP 服务]
    RemoteClient[远程 MCP 客户端] -->|HTTPS + Bearer JWT| Auth[认证与身份上下文]
    Auth -->|MCP Streamable HTTP| Server
    Server --> Tools[MCP 工具处理器]
    Tools --> Query[统一查询服务]
    Query --> Pool[连接池与数据源驱动]
    Pool --> Sources[(已配置的数据源)]
    Tools --> Translate[SQL 方言转换器]
~~~

MCP 服务作为 Data Fabric 进程的一种启动模式运行。它复用现有数据源存储、连接池和查询服务；方言转换是可选能力。stdio 由本机客户端启动和管理进程；远程 Streamable HTTP 对每个请求验证 Bearer JWT，再将身份传入工具执行和数据源授权。远程链路需使用 HTTPS，可由服务直接终止 TLS 或由 loopback 前方的反向代理终止。
