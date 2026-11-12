# 数据源管理界面

Data Fabric 在服务端提供自托管管理页面，地址为 /ui/。页面随服务二进制嵌入发布，并通过同源管理 API 支持数据源列表、创建、编辑、连接测试、启用、停用和删除。

English guide: [Data-source Management UI](data-source-management-ui.md)。

## 访问与认证

打开部署的 HTTPS 地址并在末尾添加 /ui/；本地开发环境也可使用对应的 HTTP 地址。启用了管理认证时，在页面中粘贴已有 Bearer JWT。数据源的所有者、租户和管理员权限仍由后端 API 检查；页面不签发令牌，也不绕过认证。令牌只保留在当前页面中，关闭或刷新页面时会清除。

生产环境应保持 security.enable_auth 为 true，通过 HTTPS 提供页面，并沿用部署已有的网络访问控制。HTML、JavaScript 和 CSS 来自同一服务源，响应包含限制性内容安全策略和 Cache-Control: no-store。

## 配置数据源

1. 选择“添加数据源”，并从服务端支持目录中选择已注册且受支持的连接器。
2. 填写常用连接字段。连接器专属字段可以放在“额外参数”JSON 对象中。
3. 保存前使用“测试连接”。该操作只测试当前表单配置，不会保存数据源。
4. 编辑已有数据源时，服务器不会返回已保存的密码或敏感额外参数。密码留空会保留原值；填写新值会替换原值。如果连接测试需要原密码，需要在当前表单中重新输入。
5. 在列表行中编辑、启用或停用、删除数据源。删除前会弹出确认。

表单采用通用连接字段。部分连接器需要通过“额外参数”提供专属配置；此页面不包含 Schema 浏览和查询编辑功能。

## 完成首次授权查询

1. 先确定数据源的 owner 和 tenant 身份，使用满足任务需要的最小只读数据库账号。保存后确认数据源已启用。
2. 按[自托管 MCP 部署指南](mcp-deployment-zh.md)连接远程 MCP endpoint。Claude Code 示例：

   ```bash
   export MCP_HOST=localhost
   : "${MCP_ACCESS_TOKEN:?Set an OIDC access token first}"
   claude mcp add --transport http data-fabric "https://$MCP_HOST:8443/mcp" --header "Authorization: Bearer $MCP_ACCESS_TOKEN"
   ```

   将主机名和端口改为实际 MCP endpoint。MCP_ACCESS_TOKEN 必须由配置的 OIDC identity provider 签发，且 audience、scope 和 tenant claim 符合服务端配置。该命令会把 header 写入本机 MCP 客户端配置，请保护该文件。token 过期后从 identity provider 重新签发并更新连接。浏览器上方填写的管理 JWT 与 MCP token 是不同凭据，不能互换。本地自签名证书需按部署指南设置 NODE_EXTRA_CA_CERTS；不要关闭 TLS 校验。
3. 在 MCP 客户端中依次调用 list_data_sources、list_tables、describe_table，确认当前 principal 能看到的表和列。
4. 调用 execute_sql_query 执行有界只读 SQL，并按任务选择文档支持的分页或流式模式。默认本地 synthetic orders 示例为 SELECT customer, amount FROM orders ORDER BY amount DESC；外部评估时应使用经批准的任务和数据范围。
5. 用第二个应用的独立 OIDC principal 重复任务，并按获准策略映射到同一 tenant。第二个应用应复用已登记数据源和策略，不应获得数据库用户名或密码。再用未授权 tenant 验证数据源和行不可见。若配置了表/列策略，也要确认受限对象不可见或会被拒绝。

至少邀请两名不熟悉仓库内部实现的操作者，在同一环境中分别使用基线 artifact 和候选 artifact 完成同一任务。记录每个 artifact 的 tag/digest、客户端版本、主动操作时间与总耗时、失败尝试、预期/实际结果和维护者介入情况。合成演练证据应与模型质量、真实团队采用和客户价值分开标注。使用合成数据或明确获准的数据；记录中不放密钥和原始数据。执行时请遵循[独立首次使用演练协议](validation/independent-first-use-zh.md)，锁定 artifact、控制镜像传输并记录每轮结果。

## 安全排障与 correlation ID

| 失败情况 | 安全的下一步 |
| --- | --- |
| 保存前连接测试失败 | 确认 API 容器能够解析并访问数据源主机，再检查端口、TLS 信任和凭据。连接健康前不要注册该来源。 |
| MCP 返回 401 或会话过期 | 从已配置的 identity provider 重新签发满足 issuer、audience 和 scope 要求的 access token，再连接 MCP client。管理 JWT 不能用作 MCP token。 |
| 数据源不存在或访问被拒绝 | 确认数据源已启用，并核对 OIDC subject、tenant 映射与预期 owner 和策略一致。请管理员按最小范围调整策略；不要换管理员身份绕过检查。 |
| 表或列不可见或被拒绝 | 将其视为策略过滤结果。请策略负责人核对目标表和列的授权，再只用该 principal 可见的对象重试。 |
| 有界查询报告 source error | 检查 connector 健康状态，并用当前可见表和列执行只读查询。由本地服务日志排查数据源故障；报告中不要带凭据或原始行。 |

管理 REST 错误响应在顶层提供 correlationId，HTTP 请求还会返回 X-Correlation-ID 响应头。请将 ID 与 UTC 时间、API 或 MCP 工具名称及脱敏后的错误文本一起保留。部分 MCP client 不显示响应头；若客户端没有显示 ID，记录请求时间并请部署运维人员在本地日志中匹配。自托管 Compose profile 可在部署主机本地检查日志：

    docker compose logs --since=10m data-fabric-api data-fabric-mcp

不要在 issue 中分享授权 header、token、数据源配置或原始查询结果。查询 cursor 属于单个进程，进程重启后会丢失；本 UI 不提供 cursor 管理接口。

管理 UI 只负责数据源 CRUD 和连接测试。schema 发现、查询执行、租户隔离与结果续页都由已有 MCP 工具和授权契约负责，页面不会新增绕过策略的查询 API。

## 试点状态

该界面是工程参考实现。真实团队的配置时间或操作负担尚未测量。对外描述客户价值前，请按 [M4 试点证据协议](validation/real-team-pilot-evidence.md)验证这些结果。
