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

- **AI Agent：** 可选的 MCP 工具支持本地 stdio 客户端，以及使用 Bearer JWT 认证的远程 Streamable HTTP 客户端发现数据源、校验只读 SQL、执行查询并分批获取结果，详见 [MCP 使用指南](MCP_DOCUMENTATION.md)。
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
- **网关控制：** 提供 JWT 认证、请求限流、配置校验和健康检查能力。
- **Prometheus 监控：** 从 `/metrics` 抓取 HTTP 和查询指标；该端点始终需要管理员 Bearer JWT。详见[可观测性指南](docs/observability.md)。

## 数据源范围

代码库定义了下列类别的代表性数据源。具体操作和认证能力因数据源及其驱动而异。

| 类别 | 示例 |
| --- | --- |
| 关系型数据库 | MySQL、MariaDB、PostgreSQL、Oracle、SQL Server |
| 国产及兼容数据库 | OceanBase、TiDB、TDSQL、GaussDB、达梦、KingbaseES、GBase、神通、OpenGauss |
| 分析型数据库 | ClickHouse、Doris、StarRocks、Druid、Hologres |
| 云数仓与大数据平台 | Snowflake、Databricks、Redshift、BigQuery、Hive、MaxCompute |
| 湖仓表格式 | Apache Iceberg、Delta Lake、Apache Hudi |
| 对象存储与文件系统 | S3、MinIO、阿里云 OSS、腾讯云 COS、Azure Blob、HDFS |
| 文件格式 | Parquet、ORC、Avro、CSV、JSON、XML、文本 |

Apache Ozone 当前不作为受支持的数据源对外提供，因为对象操作尚未完成。驱动注册表会拒绝 Ozone 数据源配置，待操作实现并验证后再开放。详见[Ozone 支持状态](docs/ozone-support.md)。

### 驱动打包与运行占用

标准服务构建会将注册表中的驱动实现及其依赖编译进同一个可执行文件，无需按数据源单独安装运行时插件。驱动在服务启动时注册，只有实际配置并使用的数据源才会建立连接。单文件部署更直接，但可执行文件和构建依赖会随注册的驱动类型增加。

## 快速开始

### 环境要求

- 本地源码运行需要 Go 1.26.1 或更高版本。
- 构建或运行容器需要 Docker Engine。
- 已配置且可访问的元数据数据库。
- 服务能够访问计划登记的数据源。
- 通过环境变量提供 JWT 密钥和凭证加密密钥环。

### 本地运行

~~~bash
git clone https://github.com/RhinoLink-Tech/data-fabric.git
cd data-fabric
cp configs/config.yaml.example configs/config.yaml
go mod download
~~~

在 configs/config.yaml 中配置元数据数据库。生成密钥后请妥善保存，并在服务重启时继续使用相同的值。

~~~bash
export JWT_SECRET="<稳定且至少 32 字节的密钥>"
export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1
export DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64 编码的 32 字节密钥>"}'
go run ./cmd/server
~~~

JWT 密钥可用 openssl rand -hex 32 生成，凭证加密密钥可用 openssl rand -base64 32 生成。请妥善保存，并在服务重启时继续使用相同值。服务健康检查路径为 /health；部署参数、密钥管理和轮换方式见[配置指南](docs/configuration.md)。

### 使用 Docker 构建和运行

镜像使用与 `go.mod` 一致的 Go 1.26.1，默认监听 8080，并以非 root 用户运行。启动前请先创建 MySQL 元数据数据库和账号。

~~~bash
docker build -t data-fabric:local .
docker run --rm --name data-fabric \
  --add-host=host.docker.internal:host-gateway \
  -p 8080:8080 \
  -e DATABASE_HOST=host.docker.internal \
  -e DATABASE_PORT=3306 \
  -e DATABASE_DATABASE=data_fabric \
  -e DATABASE_USERNAME=data_fabric \
  -e DATABASE_PASSWORD="<元数据库密码>" \
  -e SECURITY_JWT_SECRET="<至少 32 字节且稳定的密钥>" \
  -e DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1 \
  -e DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64 编码的 32 字节密钥>"}' \
  data-fabric:local
~~~

容器镜像不包含实际配置文件，通过环境变量和默认值启动，默认端口为 8080。如果通过 SERVER_PORT 或 server.port 覆盖端口，请同步修改容器端口和端口映射。真实密钥应由部署平台注入，重启时复用相同的凭证密钥环。

## 性能预期

Data Fabric 是查询网关，不是查询加速器。相比应用直连引擎，请求会多经过一段“应用到网关”的网络链路，并增加网关侧的认证、校验、路由、结果归一化和 JSON 编码；启用 SQL 方言转换时还会增加转换处理。对于很短的查询，网关开销占比会更明显；对于耗时较长或结果量较大的查询，引擎执行与数据传输通常占更大比重。

本次基准使用本机 PostgreSQL 作为模拟数据源，生成 500 行合成数据并覆盖 14 类查询（每条路径串行采样 1,000 次、预热 20 次）；Data Fabric 进程内查询服务的 P95 耗时为 **0.466 至 1.929 毫秒**；与直连 PostgreSQL 的 P95 分位值之差为 **0.281 至 0.436 毫秒**，P50 分位值之差为 **0.192 至 0.311 毫秒**。测试不包含 HTTP、认证中间件、并发流量及客户端到网关的网络链路。这些数字是本机实测，不是延迟上限或生产环境保证。报告还列出了全部测试项、5,000 行规模测试、方法和标准基准参考；这套自建测试不代表 TPC 官方成绩。详见[性能测试报告](docs/performance/postgres-gateway-benchmark.md)。

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

## 项目文档

- [API 使用指南](docs/api_usage.md)
- [架构设计](docs/architecture.md)
- [配置指南](docs/configuration.md)
- [开发指南](docs/development.md)
- [贡献指南](CONTRIBUTING.md)
- [安全政策](SECURITY.md)
- [获取帮助](SUPPORT.md)

## 跨源查询引擎接入

需要跨源 SQL 时，Data Fabric 可通过统一适配接入兼容的联邦查询引擎。管理员可将 Trino、Presto 和 OpenLooKeng 登记为数据源，由对应协调器使用其 catalogs 和 connectors 规划并执行跨目录查询；应用仍通过 Data Fabric 查询 API 提交 SQL。

连接配置见 [API 使用指南](docs/api_usage.md)，SQL 兼容范围见[统一 SQL v1 说明](SQL_TRANSLATION_FEATURES.md)。

## 参与贡献

欢迎通过 Issue、Pull Request 和文档改进参与共建，详情见[贡献指南](CONTRIBUTING.md)。

## 许可证

本项目采用 Apache License 2.0，详见 [LICENSE](LICENSE)。
