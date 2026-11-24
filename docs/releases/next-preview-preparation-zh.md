# 下一预览版准备记录（尚未发布）

截至 2026-11-20，最新公开预览版是 [v2.1.0-preview.2](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.2)，源码提交为 `281260f6c2000419222e55a99e984b8dd5094ab8`。该提交之后合入的功能**不在该镜像中**。本文用于准备 #442，不代表已经打标签、发布或确定发布时间和版本号。

本次核对的主线基线为 `418b9848c4f03edf846f04af302d55c156e52970`。准备工作合入后最终 SHA 会变化；发布验收必须对应最终标签和公开镜像。[未发布更新日志](../../CHANGELOG.md#unreleased)描述的是候选变更，不是当前公开镜像。

## 需要验证的用户能力

- **发现与管理数据源：** MCP 数据源列表支持分页，表结构搜索支持在授权范围内续查，编辑数据源时可以使用服务端保留的凭据测试连接。MariaDB 和 openGauss 浏览器接入是默认关闭的候选功能，需要相应驱动、校验证书的 TLS 与网络限制；尚未完成真实 WorkBuddy 客户端和发布镜像验收（[#761](https://github.com/RhinoLink-Tech/data-fabric/issues/761)、[#781](https://github.com/RhinoLink-Tech/data-fabric/issues/781)）。
- **查询与 SQL 转换：** 仓库中的 Skill 可独立调用 SQL 方言转换而不执行查询。MCP 首次查询默认每页 100 行，可明确请求最多 10,000 行，续页保留有效页大小。无总数模式需显式选择，目前限 MySQL、MariaDB、PostgreSQL 和 Trino；未知总数以 `null` 表示，默认仍为精确计数。复杂 Trino 分页语法可能被明确拒绝。联邦验收使用 Trino 484 连接合成 MySQL/PostgreSQL，不代表所有数据源或客户端均已验证（[#450](https://github.com/RhinoLink-Tech/data-fabric/issues/450)）。
- **本地部署：** Core 和 SQLite 元数据是可选实验候选，尚未作为支持的配置发布。SQLite Compose 候选只监听本机，不能据此认定公网 WorkBuddy OAuth、生产可用或升级路径已经完成（[#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)、[#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448)）。

上述基线之后，部分基于元数据的 MCP 表发现可返回与原列表对应、经过授权过滤的表身份信息，包含可用的 schema（#525）。独立 SQL 转换增加已公布方言别名规范化（#645）、同方言输入校验（#644）以及输入错误与运行故障区分（#647）。这些尚未发布；新的 HTTP 状态码和 MCP 方言校验需要客户端兼容性核对。

基线之后，数据源发现还可在适用时返回相对当前用户的所有权信息，并提供单独授权的可管理数据源列表（#818、#820）。浏览器接入流程失效或不可用时会提示从 MCP 客户端重新发起（#823）；配置审计记录器时，成功的工具发现也会记录审计事件（#822）。MySQL、MariaDB 和 PostgreSQL 分页支持单条 SQL 末尾的分号；依赖服务端模式的反斜杠转义和 MySQL 可执行注释仍不支持分页改写（#736、#737）。这些变更尚未发布。

2026-11-23 的 WorkBuddy 5.11.3 实测使用公开连接器，通过可信 HTTPS 连接本地构建的 53a6027d 网关，验证了合成数据的数据源发现、表结构、SQL 校验和只读查询（[#504](https://github.com/RhinoLink-Tech/data-fabric/issues/504)）。该实测没有验证当前主线、已发布网关镜像、分页或完整数据源生命周期；间歇性工具发现问题仍在调查（[#822](https://github.com/RhinoLink-Tech/data-fabric/issues/822)）。

仓库 Skill 变化不会自动更新已发布的 WorkBuddy 市场条目。WorkBuddy 授权续期与独立用户首次接入、完整数据源生命周期和外部团队价值仍需分别验收（[#504](https://github.com/RhinoLink-Tech/data-fabric/issues/504)、[#541](https://github.com/RhinoLink-Tech/data-fabric/issues/541)、[#603](https://github.com/RhinoLink-Tech/data-fabric/issues/603)、[#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446)、[#458](https://github.com/RhinoLink-Tech/data-fabric/issues/458)）。

在上述源码基线之后，#797 增加了默认关闭的 MCP OAuth 旧受众过渡配置，#799 修正了身份一致性诊断，#800 增加了无需凭据的公网发现诊断。这些是源码层面的新增能力，尚不包含在 preview.2 中。合成令牌和诊断测试不能证明真实身份服务的受众配置、客户端授权或令牌续期已经完成；现有公网网关也尚未切换。进展见 [#608](https://github.com/RhinoLink-Tech/data-fabric/issues/608)。

## 候选证据和剩余步骤

- 基线的 [Go CI](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/44686671743)、[Core 候选](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/44687156049)和 [SQLite 候选](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/44687156094)对应上述主线 SHA。使用证据前应查看每项运行的最终结论和产物，跳过或未完成不等于通过。前一个 PR 的联邦 HTTP 验收见[运行记录](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/44685131621)。
- 源码提交 20d6f23d0e72341223779a70ecd531312e611981 的[隔离合成环境恢复验收](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/45306259074)已通过。单独的 preview.2 源码包和镜像演练使用的是当前恢复工具，而不是 preview.2 自带的旧工具；这些结果不代表已发布新制品或完成特定部署的升级验收。
- 确定发布版本和支持的部署拓扑；解决安全与兼容阻塞，复核 issue 状态并记录延期 P1 的风险决定。现有完整 MySQL 元数据配置与实验 Core/SQLite 候选的证据不同，不能混同其支持范围。
- 冻结最终源码，更新日志和安装指南，并验证精确 SHA 对应的构建、许可材料、所选数据源、REST/MCP 和恢复路径。针对拟发布制品验证升级和恢复；#522 的历史密钥凭据预检已合入，但仍须单独验证与发布制品匹配的恢复流程。不能假定旧预览版源码归档的恢复结论适用于新归档。
- 按[发布流程](../releasing.md)对审定提交打标签，验证公开镜像来源与摘要，使用该精确摘要完成无 Go 安装及 REST/MCP 验收，校验下载的源码包和校验文件并记录实际结果。公开制品、独立用户验收和市场更新需要各自的证据。

本记录不代表已完成性能基准、广泛版本兼容、托管服务自助开通或 WorkBuddy 市场更新。