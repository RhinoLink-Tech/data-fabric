# Data Fabric：面向 AI 应用的轻量数据接入网关

<div align="center">
  <img src="assets/data-fabric-icon.png" alt="Data Fabric 图标" width="160">
  <p><strong>面向 AI Agent、RAG 与智能分析的轻量数据网关：统一接入多源，让应用用一套 SQL 查询常见数据。</strong></p>
  <p><a href="README.md">English</a></p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.26.1+-00ADD8?logo=go" alt="Go 1.26.1+">
    <img src="https://img.shields.io/badge/License-Apache--2.0-blue" alt="Apache 2.0">
    <a href="https://github.com/RhinoLink-Tech/data-fabric/actions/workflows/go.yml"><img src="https://github.com/RhinoLink-Tech/data-fabric/actions/workflows/go.yml/badge.svg" alt="Go CI"></a>
  </p>
</div>

⭐ [欢迎为 Data Fabric 点亮 Star](https://github.com/RhinoLink-Tech/data-fabric) · 🚀 [快速开始](#快速开始) · 🤝 [参与贡献](CONTRIBUTING.md)

你是否正在构建 AI Agent、RAG 流程或智能分析功能，需要从多个企业数据系统中获取上下文？每种数据源各自的连接方式、认证配置、SQL 方言和结果格式，都会增加 AI 应用的集成工作。

Data Fabric 为 AI 应用提供轻量、自托管的异构数据访问层。团队在一个入口集中管理连接，应用则通过统一 API 和 SQL 接口查询已登记的数据源。它连接 AI 工作负载与现有数据系统，不接管模型推理和 AI 流程编排。

## AI 应用场景

- **AI Agent：** 可选 MCP 工具支持本地 stdio 和远程 Streamable HTTP，帮助发现数据源、校验只读 SQL、执行查询并分批取数。release 模式的远程 MCP 使用 OIDC access token；本地 JWT 模式仅用于开发。详见 [MCP 使用说明](MCP_DOCUMENTATION.md)和[自托管部署指南](docs/mcp-deployment-zh.md)。
- **RAG 检索：** 从数据库、数仓、对象存储及已支持的文件源获取结构化或半结构化记录；切分、Embedding、向量存储和排序仍由 RAG 应用负责。
- **Text-to-SQL：** 模型生成的只读查询可以经统一 API 提交，并在兼容范围内转换为目标数据源方言。
- **AI 智能分析：** 让助手通过统一入口访问已登记的数据源；需要跨源 SQL 时再连接联邦查询引擎。

Data Fabric 为 AI 应用提供数据访问层；模型、提示词、向量化流程和向量检索由 AI 技术栈负责。

## 为什么选择 Data Fabric

- **集中管理数据源：** 通过统一入口登记和维护数据源连接、查看配置并诊断连通性。
- **统一应用查询方式：** 通过一致的 API 提交查询并获取结果；SQL 方言转换范围见[统一 SQL v1 说明](SQL_TRANSLATION_FEATURES.md)。
- **按需保持轻量：** 单源访问可直接使用网关驱动，无需为了简单查询额外部署联邦引擎；需要跨源 SQL 时再接入。
- **数据留在原系统：** Data Fabric 提供访问层，不负责复制数据或运行 ETL；联邦查询仍会在系统之间传输查询请求和中间结果。
- **适配企业与集群的混合环境：** 用同一个应用侧入口访问数据库、数仓、湖仓、对象存储和文件。

## SQL 标准与兼容范围

目前已发布的国际 SQL 标准版本为 [ISO/IEC 9075:2023（SQL:2023）](https://www.iso.org/standard/76584.html)。实际使用中，各数据库实现的标准特性范围不同，也会增加各自的方言语法；仅使用标准名称并不能保证任意 SQL 都能跨库运行。可参考 [PostgreSQL](https://www.postgresql.org/docs/current/features.html) 和 [MySQL](https://dev.mysql.com/doc/refman/8.4/en/compatibility.html) 的 SQL 兼容说明。

Data Fabric 的 SQL v1 是面向常见应用查询的统一只读语法子集，不另造一套 SQL，也不宣称完整符合 SQL:2023。它覆盖 SELECT 和 UNION、投影与基本表达式、WHERE、INNER/LEFT JOIN、GROUP BY 与聚合、HAVING、ORDER BY、LIMIT/OFFSET；常见标量及日期时间构造以兼容矩阵中的样例为准。

需要跨方言转换时，通过 sourceDialect 声明输入 SQL 的方言，Data Fabric 按[兼容矩阵](SQL_TRANSLATION_FEATURES.md)转换为目标数据源方言。省略 sourceDialect 时，SQL 按目标引擎原生语法直接提交，不进行方言转换。引擎专有函数、CTE、窗口函数、复杂类型和时间旅行语法没有统一转换保证；类型转换、NULL 排序、时区、精度等执行语义仍由目标引擎决定。

## Data Fabric 在架构中的位置

| 组成部分 | 负责什么 |
| --- | --- |
| Data Fabric | 面向应用的 API、数据源连接管理、诊断、查询路由和结果获取 |
| 联邦查询引擎（可选） | 通过 catalogs 和 connectors 访问多个系统，并规划、执行跨源 SQL |
| 原始数据系统 | 保存和管理实际数据 |

Data Fabric 是面向应用的网关。它不替代原有数据库，不是数仓，也不重新实现分布式查询引擎。

~~~mermaid
flowchart LR
    App[AI / RAG / BI / 业务应用] --> Gateway[Data Fabric<br/>统一 API 与数据源管理]
    Gateway --> Drivers[单源数据源驱动]
    Drivers --> Sources[数据库 / 数仓 / 湖仓 / 对象存储 / 文件]
    Gateway --> Adapter[查询引擎连接适配]
    Adapter --> Engine[外部联邦查询引擎]
    Engine --> Catalogs[引擎 Catalogs 与 Connectors]
    Catalogs --> Sources
~~~

应用可以通过网关驱动访问单个数据源，也可以将 SQL 发往外部联邦引擎执行跨目录查询。

## 核心能力

- **数据源生命周期管理：** 创建、查看、更新、启用、停用和删除数据源。
- **驱动注册与扩展：** 通过统一驱动接口扩展数据源类型。
- **连接诊断：** 测试连接、校验配置，并查看连接健康状态和连接池统计。
- **统一查询 API：** 提交 SQL 并分批获取结果；支持流式读取的驱动可降低大结果集的一次性内存占用。
- **SQL 方言适配：** 将兼容范围内的查询语法转换为目标方言，详见[统一 SQL v1 兼容矩阵](SQL_TRANSLATION_FEATURES.md)。
- **安全与审计：** 提供 JWT API 认证、远程 MCP 的 OIDC 身份校验、所有者/租户及表列级访问策略、脱敏审计事件、只读 SQL 校验，并在支持的数据库会话中启用只读限制；PostgreSQL、openGauss、MySQL、MariaDB 和 TiDB 连接支持服务端证书和主机名校验，同时提供请求限流、配置校验和健康检查。
- **Prometheus 监控：** 从 `/metrics` 抓取 HTTP 和查询指标；该端点始终需要管理员 Bearer JWT。详见[可观测性指南](docs/observability.md)。

## 数据源范围

Data Fabric 通过数据库类型和兼容性 API 发布共享的数据源支持目录。目录区分已登记实现与端到端验证、声明的版本范围与精确测试版本，也区分协议 fixture 与真实服务证据。版本范围为空表示尚未建立兼容范围；连接测试成功本身不代表已完成端到端验证。部分已登记实现仍处于实验状态。选择连接器前请查看[数据源支持目录](docs/data-source-support.md)和[Ozone 支持状态](docs/ozone-support.md)。

### 驱动打包与运行占用

标准服务构建会将注册表中的驱动实现及其依赖编译进同一个可执行文件，无需按数据源单独安装运行时插件。驱动在服务启动时注册，只有实际配置并使用的数据源才会建立连接。单文件部署更直接，但可执行文件和构建依赖会随注册的驱动类型增加。

## 快速开始

当前公开预览版为 [v2.1.0-preview.1](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1)，提供 digest 固定的 `linux/amd64` 镜像。建议按[自托管远程 MCP 部署指南](docs/mcp-deployment-zh.md)中的发布镜像路径安装；完整 digest 和验收范围见 Release。源码构建仍适用于开发和本地修改。

示例配置使用 MySQL 元数据数据库，并由 REST 与 MCP 两个独立进程共享；预置数据为合成数据。它演示已记录的路径，不证明外部团队采用、生产容量或 SLA。请同时查看[首个采用者画像](docs/product/first-adopter-profile.md)（当前仍是待验证假设）、[数据源支持目录](docs/data-source-support.md)和[当前运行边界](docs/roadmap.md#current-operating-limits)。

该 Preview profile 面向单机部署，不承诺 HA、SLA 或生产就绪。发布镜像路径要求 Docker Engine 24 或更高版本、Docker Compose 2.24 或更高版本、OpenSSL、带标准库的 Python 3，以及能够为已配置受众和查询 scope 签发 access token 的 OIDC 身份提供方；无需在宿主机安装 Go 或 Python 第三方包。

### 从源码运行网关用于开发

直接从源码运行需要 Go 1.26.1 或更高版本、Python 3、可访问的 MySQL 元数据数据库，以及到计划使用的数据源的网络连通性。只读 SQL 分析和方言转换默认使用 SQLGlot 运行时。从仓库根目录在虚拟环境中安装锁定的 parser 依赖：

~~~bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-sql-translation.txt
go mod download
cp configs/config.yaml.example configs/config.yaml
~~~

在 configs/config.yaml 中设置元数据数据库字段。通过环境变量或密钥管理器注入稳定的 JWT 密钥和凭证密钥环；服务重启时必须继续使用相同密钥环，才能解密已保存的数据源凭证。

~~~bash
export SECURITY_JWT_SECRET="<稳定且至少 32 字节的密钥>"
export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1
export DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64 编码的 32 字节密钥>"}'
go run ./cmd/server
~~~

可使用 openssl rand -hex 32 和 openssl rand -base64 32 生成密钥；应安全保存，切勿在每次重启时重新生成。管理 API 的配置及查询请求见 [API 使用指南](docs/api_usage.md)。部署受保护的远程 MCP 时，请使用上面的 OIDC profile；REST 管理 JWT 与身份提供方签发的 MCP access token 是两种不同凭据。

## 性能预期

Data Fabric 是查询网关，不是查询加速器。相比应用直连引擎，请求会多经过应用到网关的网络链路，并增加网关侧的认证、校验、路由、结果归一化和 JSON 编码；启用 SQL 方言转换时还会增加转换处理。对于很短的查询，网关开销占比会更明显；对于耗时较长或结果量较大的查询，引擎执行与数据传输通常占更大比重。

在本机 WSL 环境中使用 PostgreSQL 模拟数据源，以 500 行合成数据和 14 类查询进行测试，Data Fabric 进程内查询服务的 P95 耗时为 0.682–2.159 毫秒；与直连 PostgreSQL 的 P95 分位值之差为 0.446–0.694 毫秒，P50 分位值之差为 0.246–0.450 毫秒。另有一组 5,000 行规模测试，包含内连接和左连接、聚合、宽结果和无索引排序；完整报告提供 CPU、内存分配和原始采样数据。本地串行测试不包含 HTTP、认证中间件、并发流量及客户端到网关的网络链路，不构成生产环境保证。这套自建测试也不代表 TPC 官方成绩。详见[性能测试报告](docs/performance/postgres-gateway-benchmark.md)。

另外提供本地部署资源基线：在该 WSL2 实测环境中，服务重启至健康检查通过的中位耗时为 0.488 秒，空闲 RSS 中位值为 83.6 MiB。报告列出 5 万行分批流式场景、构建产物体积、原始样本和复现方法。这是单机本地数据，不构成生产保证。详见[部署资源基线报告](docs/performance/deployment-footprint.md)。
## API 概览

| 方法 | 路径 | 用途 | 认证 |
| --- | --- | --- | --- |
| GET | /health | 服务健康检查 | 无 |
| GET | /api/v1/database/types | 查询服务识别的数据源类型 | 无 |
| GET | /api/v1/database/compatibility | 查看数据库兼容性信息 | 无 |
| GET / POST | /api/v1/datasources | 列出或创建数据源 | 开启认证时需要 Bearer；按所有者/租户隔离 |
| GET / PUT / DELETE | /api/v1/datasources/:id | 查看、更新或删除数据源 | 同上；非管理员对不存在与他人数据源返回统一的 403 |
| POST | /api/v1/datasources/:id/activate | 启用数据源 | 同上 |
| POST | /api/v1/datasources/:id/deactivate | 停用数据源 | 同上 |
| POST | /api/v1/database/test-connection | 测试数据源连接 | 开启认证时需要 Bearer |
| POST | /api/v1/database/validate-config | 校验数据源配置 | 开启认证时需要 Bearer |
| GET | /api/v1/database/health | 全量数据源健康探测 | 开启认证时仅限 admin 角色 |
| GET | /api/v1/database/connections/stats | 连接池统计 | 开启认证时仅限 admin 角色 |
| POST | /api/v1/fetch | 执行查询并获取首批结果 | 始终需要 Bearer；需对目标数据源有读权限 |
| GET | /api/v1/fetch/:query_id/:slug/:token | 获取后续结果批次 | 始终需要 Bearer；会话仅创建者可续拉 |

请求字段、响应结构和认证行为见 [API 使用指南](docs/api_usage.md)，数据源授权模型见 [配置文档](docs/configuration.md#data-source-authorization)。
自托管的数据源管理页面位于 /ui/，使用说明见[数据源管理界面文档](docs/data-source-management-ui-zh.md)。

## 项目文档

- [API 使用指南](docs/api_usage.md)
- [数据源管理界面](docs/data-source-management-ui-zh.md)
- [DeepSeek API 函数调用示例](examples/deepseek-agent/README.md)
- [架构设计](docs/architecture.md)
- [产品路线图](docs/roadmap.md)
- [采用与产品价值执行计划](docs/plans/gateway-delivery.md)
- [首个采用者与支持画像](docs/product/first-adopter-profile.md)（英文；当前假设，外部验证仍待完成）
- [开源采用与产品价值演进计划](docs/plans/gateway-delivery.md#productization-program)（交付与验证规划中）
- [配置指南](docs/configuration.md)
- [开发指南](docs/development.md)
- [贡献指南](CONTRIBUTING.md)
- [安全政策](SECURITY.md)
- [获取帮助](SUPPORT.md)

## 跨源查询引擎接入

需要跨源 SQL 时，Data Fabric 可通过统一适配接入兼容的联邦查询引擎。Trino、Presto 和 OpenLooKeng 适配器已实现并可登记为数据源。支持目录记录版本固定的驱动契约证据，目前仍将这三个适配器标为“已实现”。另有可选的 [Trino 484 集成验证配置](docs/validation/trino-integration.md)，通过公开 REST 和 MCP 接口覆盖报告列出的只读跨目录查询、超时、协调器恢复和引擎错误路径。该有限范围不证明其他 Trino 版本、认证方式、连接器、SQL 功能、worker 拓扑或生产工作负载的兼容性。

连接配置见 [API 使用指南](docs/api_usage.md)，SQL 兼容范围见[统一 SQL v1 说明](SQL_TRANSLATION_FEATURES.md)。

## 参与贡献

欢迎通过 Issue、Pull Request 和文档改进参与共建，详情见[贡献指南](CONTRIBUTING.md)。

## 许可证

本项目采用 Apache License 2.0，详见 [LICENSE](LICENSE)。
