# 实验性本机 SQLite 配置

[English](sqlite-first-use.md) · [SQLite 原型](sqlite-metadata-prototype-zh.md) · [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

这是使用尚未发布的 SQLite/Core 镜像进行的**本机隔离开发评估**。REST 和 TLS MCP 仅绑定宿主机回环地址，使用短期本机 JWT；它不提供 WorkBuddy 公共平台的 OAuth 自动授权。SQLite 仅用于 debug 模式，不是受支持的生产或高可用方案。Core 包含 MySQL、MariaDB、PostgreSQL 和 Trino，不包含完整连接器目录。

需要 Linux、Docker Compose、Python 3 和 OpenSSL。本机构建会下载基础镜像及依赖；也可以通过远端 CI 验证镜像。请使用隔离机器与数据源。已有 Linux CI 结果不能代表 Docker Desktop 等其他环境。

在仓库根目录执行：

```sh
cd deploy/sqlite
python3 ../../scripts/sqlite/init.py
docker compose --project-name data-fabric-sqlite up --build --detach --wait
curl --fail http://127.0.0.1:8080/health
```

初始化会生成仅属主可访问的 `.env` 和 `tls/` 中的本机证书，并保留已有的有效配置。如果元数据卷已存在但 `.env` 丢失，它会停止，避免默默生成不兼容的密钥。请单独保护并备份原有 `.env` 和凭证密钥环：丢失密钥会导致已存的数据源凭证无法读取。TLS 证书为自签名证书，客户端必须显式信任 `tls/tls.crt`，不要关闭证书验证。两个端口默认只绑定本机，可在受保护的 `.env` 中修改。

为 REST 和 MCP 使用的同一身份生成本机测试令牌：

```sh
DF_TOKEN="$(python3 ../../scripts/mcp/onboard.py token --directory . --user local-operator --tenant local)"
curl --fail -H "Authorization: Bearer $DF_TOKEN" http://127.0.0.1:8080/api/v1/datasources
```

令牌默认有效 15 分钟，不会自动刷新；过期后重新执行生成命令。不要将令牌发到聊天、提交仓库或写入共享日志。MCP 地址为 `https://localhost:8443/mcp`，使用相同令牌。具体客户端能否信任此证书并配置令牌，需要在客户端内验证。这个配置不实现 WorkBuddy 公共平台的自动 OAuth 授权。

通过 REST 注册已授权的只读数据源，再通过 REST 或 MCP 查询；参见 [API 使用说明](../api_usage.md)和 [MCP 工具说明](../mcp-tools.md)。访问 Docker 宿主机上的数据库时可使用 `host.docker.internal`；数据库必须能被容器访问，账号应只拥有目标表的读取权限。

停止服务但保留元数据：

```sh
docker compose --project-name data-fabric-sqlite down
```

除非明确要丢弃元数据，否则不要加 `--volumes`。备份恢复参见[原型说明](sqlite-metadata-prototype-zh.md#实验性备份与恢复)，并将快照和密钥备份与运行卷分开保存。远端 CI 验证合成 PostgreSQL、TLS MCP、跨租户拒绝、快照恢复和重启；它不代表所有数据库、客户端、平台或灾难恢复场景均已验证。

## 备份与恢复 Compose 元数据卷

在 `deploy/sqlite` 目录中创建仅自己可访问的宿主机目录，将运行中数据库的一致性快照保存到新文件。镜像必须已存在于本机，命令不会拉取镜像。快照包含敏感元数据，请妥善保护；原 `.env` 和凭证密钥环需要另行备份，快照不包含解密数据源凭证所需的密钥。

```sh
mkdir -m 700 -p "$HOME/data-fabric-backups"
python3 ../../scripts/sqlite/volume_snapshot.py backup "$HOME/data-fabric-backups/metadata.sqlite"
```

输出文件已存在时命令会拒绝覆盖。如果快照操作在卷内创建文件后失败，请先检查卷再重试。非默认项目可在 `backup` 前添加 `--project-name NAME`；元数据文件名不是默认值时添加 `--database NAME.sqlite`。脚本仅支持元数据卷根目录下的文件。

恢复前停止两个服务和其他所有可能访问该卷的进程。使用原 `.env` 和密钥环，将快照恢复为新文件名；脚本不会替换原数据库。使用前请在隔离环境验证恢复结果。以下示例恢复到原卷，因此宿主机快照仍需单独保留：

```sh
docker compose --project-name data-fabric-sqlite down
python3 ../../scripts/sqlite/volume_snapshot.py restore "$HOME/data-fabric-backups/metadata.sqlite" --output-name restored.sqlite
# 后续隔离启动时设置 DATA_FABRIC_SQLITE_PATH=/var/lib/data-fabric/restored.sqlite。
```

脚本会检查所选 Compose 项目的运行中服务，无法发现其他项目或进程对该卷的访问。完成恢复不代表密钥环、数据源连接或灾难恢复已经验证；隔离验证之前请保留原元数据和密钥。
