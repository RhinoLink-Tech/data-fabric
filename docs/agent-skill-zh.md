# 在 Codex 和 WorkBuddy 中使用 Data Fabric Skill

[English](agent-skill.md)

两个客户端共用 [`data-fabric-query`](../skills/data-fabric-query/SKILL.md)。MCP 提供数据源、表结构和查询工具，Skill 指导客户端选择数据源、检查字段、校验只读 SQL，并按需取回结果。

## 准备连接

准备客户端可访问的 HTTPS `/mcp` 地址、已有读取权限的数据源，以及身份提供方签发的访问 token。token 的 issuer、audience 和 scope 必须符合网关配置，默认 scope 为 `data-fabric.query`。测试证书需要客户端信任。使用 JWT 访问 token，管理 token 用于 REST API。访问 token 过期后从身份提供方重新获取。部署和 token 配置见 [MCP 部署指南](mcp-deployment.md#5-prepare-management-and-mcp-access-tokens)。

下载 Data Fabric 仓库或发布源码包，保留完整的 `skills/data-fabric-query/` 目录，包括 `SKILL.md`、`references/` 和 `LICENSE.txt`。

## Codex

1. 将整个 `data-fabric-query/` 目录复制到要使用该技能的项目的 `.agents/skills/` 下。项目内的路径应为 `.agents/skills/data-fabric-query/SKILL.md`。
2. 在启动 Codex 的环境中设置 `MCP_ACCESS_TOKEN`。将实际 HTTPS 地址替换到 [Codex 配置示例](../examples/mcp-clients/codex.config.toml)，再把该配置段加入 Codex 的 `config.toml`。也可以在 Codex CLI 中执行以下命令添加同一个连接。

   ```sh
   codex mcp add data-fabric --url https://data-fabric.example.com/mcp --bearer-token-env-var MCP_ACCESS_TOKEN
   ```

3. 在项目中开启新会话，输入 `$data-fabric-query`，再写下查询任务。使用 `/mcp` 检查工具是否可用。如有工具审批提示，核对本次只读调用后再批准。

[Codex Skill 文档](https://learn.chatgpt.com/docs/build-skills) · [Codex MCP 文档](https://learn.chatgpt.com/docs/extend/mcp)

## WorkBuddy

1. 从 Data Fabric 仓库根目录打包 Skill。ZIP 顶层目录为 `data-fabric-query/`。

   ```sh
   python3 -m zipfile -c data-fabric-query.zip skills/data-fabric-query
   ```

2. 从左侧打开 **专家·技能·连接器 → 技能**，再选择 **添加技能 → 上传技能 → 选择 ZIP 文件**。安全检测结果显示「安全」后，如有提示，点击 **继续安装**。确认 `data-fabric-query` 出现在已安装技能列表中。
3. 打开 **连接器 → 自定义连接器**，按 [WorkBuddy 配置示例](../examples/mcp-clients/workbuddy.mcp.json) 设置 `streamableHttp` 和实际的 `/mcp` 地址。仅在本机连接器配置中填入 `Authorization` 的 `Bearer <OIDC_ACCESS_TOKEN>` 值。验证连接并启用。
4. 在新会话中选择 `data-fabric-query`，或明确要求使用它，并写下查询任务。

[WorkBuddy 技能上传](https://intl.cloud.tencent.com/zh/document/product/1316/84503) · [WorkBuddy 自定义连接器](https://intl.cloud.tencent.com/zh/document/product/1316/84494) · [MCP 配置格式](https://open.workbuddy.cn/docs/connector)

## WorkBuddy 市场发布材料

项目提供了 [WorkBuddy 连接器和独立 Skill 的候选发布包生成方法](../integrations/workbuddy/README.md)。包使用用户自填的 MCP 地址和访问令牌；发布前仍需完成平台审核及 WorkBuddy 客户端端到端验收。

## 用合成订单验收

按 [MCP 部署指南](mcp-deployment.md#quickstart) 启动独立的 MySQL 示例并注册 `sample-mysql`。给测试身份授权读取 `sample.orders`。在每个客户端中运行同一个任务。

> 使用 data-fabric-query，从已连接的 Data Fabric MCP 查找 MySQL 示例数据源，检查 orders 表和 id 字段。先校验只读 SQL，再按 id 升序查询全部订单 ID，首批和续页的 batch_size 都设为 2。报告订单 ID 和结果是否完整。

验收时查看客户端工具调用，确认发现数据源、列出表、描述字段、校验成功、执行查询和续页都已完成。首批应返回 `[1, 2]` 并带有 `nextUri`，续页返回 `[3]` 且 `nextUri` 为空或缺失，最终答案为 `[1, 2, 3]`，结果完整。记录客户端版本、网关镜像版本和每批行数。

若缺少工具，先检查连接器是否启用、地址是否以 `/mcp` 结尾，以及证书是否受信任。若连接返回 401，检查 token 是否过期及 issuer、audience、scope；连接正常但看不到样例时，检查该身份的数据源读取权限。错误排查见 [MCP 工具参考](mcp-tools.md)。
