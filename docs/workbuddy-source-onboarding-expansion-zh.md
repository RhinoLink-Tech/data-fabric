# 扩展 WorkBuddy 数据源安全接入

本文是规划，不代表新类型已经可以在 WorkBuddy 自助接入。当前源码中的可选浏览器向导默认仅允许 MySQL 和 PostgreSQL，MariaDB 实验能力需要管理员显式开启；安装 Skill 或连接器也不会自动部署 Data Fabric 服务。详见 [WorkBuddy 接入说明](../integrations/workbuddy/README.md)。

用户目标是在 WorkBuddy 中发起接入，在独立认证的浏览器页面填写数据库凭据，完成连接测试、表结构发现和只读查询；数据库密码不进入聊天或 MCP 工具参数。可以先用合成订单数据验证流程，但合成场景不等同于真实客户需求或 WorkBuddy 客户端验收。

| 后续候选 | 当前依据 | 下一步 |
| --- | --- | --- |
| MariaDB | 指定版本已有驱动与 TLS 合约验证，采用 MySQL 协议。 | [#761](https://github.com/RhinoLink-Tech/data-fabric/issues/761) 的默认关闭候选已加入，真实 WorkBuddy 客户端和完整生命周期仍需验收，也需要确认实际用户需求。 |
| openGauss | 指定版本已有查询、元数据、只读会话及 TLS 验证。 | 有明确使用需求后，独立验证 PostgreSQL 兼容路径与安全配置。 |
| TiDB | 指定版本已有驱动与 TLS 验证。 | 当前通用只读会话初始化不适用，必须先验证数据库账号的最小权限及目标部署。 |
| ClickHouse | 指定版本已有原生协议与 TLS 验证。 | 需要单独设计受限网络连接路径，不能直接复用现有协议假设。 |
| SaaS、文件和对象存储 | 不同类型的认证与资源访问方式不同。 | 分别设计授权、路径、分页及配额，不按登记数量直接开放。 |

具体验证过的版本以[数据源能力目录](data-source-support.md)和[版本策略](connector-version-policy-zh.md)为准。某一版本的驱动验证不等于同类型其他版本或浏览器接入已验证。

每新增一种类型，都要先确定用户任务和连接字段，再验证允许的网络地址和端口、固定解析地址、证书与主机名校验、数据库只读权限和访问失败处理。合成环境需覆盖新增、测试、查表、分页查询、修改、停用和删除，以及无权访问、错误凭据、错误证书和跨租户拒绝；记录源码版本与镜像摘要。最后用真实 WorkBuddy 客户端分别验收，才能宣传已经支持完整的客户端自助接入。详细工程要求见[英文方案](workbuddy-source-onboarding-expansion.md)。

另见[飞书多维表格只读数据源候选评估](feishu-bitable-evaluation-zh.md)。
