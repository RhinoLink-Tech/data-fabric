# 自托管 MCP Profile 备份与恢复

本文覆盖当前单机 profile：MySQL 元数据库、固定身份的 Data Fabric 镜像、credential-vault keyring 和 OIDC 配置。操作时使用备份清单记录的不可变候选版本。流程不备份业务源数据库、查询结果、活动游标、日志或 TLS 私钥。

备份包含 `deploy/mcp/.env` 和完整的 `data_fabric` 元数据库。加密归档必须按秘密管理：元数据行中的来源凭据虽已加密，仍依赖清单中的全部历史密钥才能解密。工具只能验证 keyring 格式，不能证明每个已存凭据都可解密；恢复验收会包含所选来源连接检查。

## 前提与密钥保管

- Python 3.10+、Docker Engine、Docker Compose 2.24+、`age`，以及与候选源码 SHA 完全一致的干净 Git 仓库检出（源码压缩包不包含可验证的提交身份）。
- 为备份准备 `age` 公钥 recipient。私钥 identity 应保存在独立恢复设备或密钥管理器中，不要与备份放在应用主机上。
- 提供仅当前用户可读写的暂存目录（`0700`），并放在加密磁盘或内存文件系统上。工具会在其中临时写入明文 SQL 和 `.env`，结束后清理；SSD 和写时复制文件系统不保证安全擦除。
- 加密备份保存到 `deploy/mcp/` 之外。复制到异机后，通过另一渠道保留校验和。

在恢复设备上创建私钥，只把公钥 recipient 提供给应用主机：

```bash
umask 077
install -d -m 700 /secure/recovery
age-keygen -o /secure/recovery/data-fabric.agekey
age-keygen -y /secure/recovery/data-fabric.agekey > /secure/recovery/data-fabric.recipients
chmod 600 /secure/recovery/data-fabric.agekey /secure/recovery/data-fabric.recipients
```

recipient 是公开值；identity 是私钥，必须与归档分开保存。

## 创建加密备份

从干净候选检出运行。先确认 `onboard.py doctor` 全部通过。工具要求 `deploy/mcp/.env` 是权限为 `0600` 的普通文件，并校验活动及保留的 32 字节密钥。加密清单记录源码 SHA、实际运行应用镜像 ID/引用、元数据库 MySQL 镜像身份/版本、key ID 和载荷校验和；工具会检查应用镜像的 OCI revision 标签与干净源码 SHA 一致。

```bash
install -d -m 700 /mnt/encrypted-staging/data-fabric /mnt/encrypted-backups/data-fabric
python3 scripts/mcp/recovery.py backup \
  --recipient "$(cat /secure/recovery/data-fabric.recipients)" \
  --staging-dir /mnt/encrypted-staging/data-fabric \
  --output "/mnt/encrypted-backups/data-fabric/profile-$(date -u +%Y%m%dT%H%M%SZ).age"
```

工具只会停止所选 Compose 项目中正在运行的 API 和 MCP 服务；完成元数据快照后会重启此前运行的服务。数据库保持在线，使用单事务 dump。快照期间不要轮换密钥、迁移 schema，也不要让第二个应用实例写入元数据库。若主机意外断电或被强制终止，需手动检查并启动这两个服务。

命令只输出加密归档校验和、候选/数据库身份及 key ID，不输出 `.env`、密钥字节、SQL dump、数据库密码或来源凭据。将校验和与归档分开记录，私钥 identity 保存在异机。

## 传输或恢复前校验

在能够访问私钥的恢复主机上解密到受保护的暂存目录，验证归档成员、清单、校验和与 keyring 清单：

```bash
python3 scripts/mcp/recovery.py verify \
  --archive /mnt/encrypted-backups/data-fabric/profile-20261108T000000Z.age \
  --identity-file /secure/recovery/data-fabric.agekey \
  --staging-dir /mnt/encrypted-staging/data-fabric
```

校验器仅输出非敏感来源信息和 key ID，之后删除临时明文文件。还要将归档 SHA-256 与复制归档时独立保存的值比对。归档校验通过不代表来源凭据可解密，也不代表外部数据库可连接；这些都要在恢复验收中确认。

## 准备全新实例恢复

使用清单 `source_revision` 指定的同一源码 SHA 创建新检出。不要在现有部署目录执行。目标 `deploy/mcp/.env` 必须不存在，`tls/` 必须不存在或为空，`recovery/` 必须未使用。命令拒绝覆盖这些文件。它会以仅所有者可读的权限写入恢复的 `.env` 和 SQL dump，只改用于隔离恢复项目的主机端口与 TLS 组 ID，不会启动容器，也不会导入数据库。

```bash
git clone https://github.com/RhinoLink-Tech/data-fabric.git /srv/data-fabric-restore
cd /srv/data-fabric-restore
git checkout <从已校验清单复制的-source_revision>
python3 scripts/mcp/recovery.py restore \
  --archive /mnt/encrypted-backups/data-fabric/profile-20261108T000000Z.age \
  --identity-file /secure/recovery/data-fabric.agekey \
  --staging-dir /mnt/encrypted-staging/data-fabric \
  --target-deploy-dir deploy/mcp \
  --project-name data-fabric-restore-20261108 \
  --api-port 18080 --mcp-port 18443
```

归档不保存 TLS 证书/私钥。运行 `onboard.py init` 会为隔离实例生成新的本地 TLS pair。若要使用正式域名，需安装对应证书并调整 TLS group。原 profile 和卷保持不变。运行恢复准备命令前，目标主机必须已有清单中精确的 app image ID：registry 候选先按清单中的 tag@sha256 拉取；本地候选需在同 SHA checkout 构建或从备份主机安全导入该镜像，并确认 ID 匹配。该备份不包含镜像层。

从仓库根目录开始生成 TLS、向新项目的空 MySQL 卷导入 dump，再启动整个 profile。项目名必须唯一，恢复检查端口仅绑定本机回环地址：

```bash
cd deploy/mcp
python3 ../../scripts/mcp/onboard.py init --directory "$PWD"
# Repeat initialization; existing keys, configuration and TLS files must be preserved.
python3 ../../scripts/mcp/onboard.py init --directory "$PWD"
export COMPOSE_PROJECT_NAME=data-fabric-restore-20261108
compose_files=(-f docker-compose.yml -f docker-compose.recovery.yml)
if grep -Eq '^DATA_FABRIC_IMAGE=.*@sha256:[0-9a-f]{64}$' .env; then
  compose_files=(-f docker-compose.yml -f docker-compose.release.yml -f docker-compose.recovery.yml)
fi
compose=(docker compose --project-name "$COMPOSE_PROJECT_NAME" --env-file .env "${compose_files[@]}")
"${compose[@]}" up --no-build -d metadata-db
db_container="$("${compose[@]}" ps -q metadata-db)"
docker inspect --format '{{.Image}}|{{.Config.Image}}' "$db_container"  # 比对清单中的 MySQL image ID/reference
"${compose[@]}" exec -T metadata-db sh -ec \
  'until mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD" --silent; do sleep 2; done'
"${compose[@]}" exec -T metadata-db sh -ec \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root' < recovery/metadata.sql
"${compose[@]}" up --no-build -d
python3 ../../scripts/mcp/onboard.py doctor --directory "$PWD"
```

恢复覆盖会把 MCP 端口也限制在 `127.0.0.1`。若 `.env` 中 `DATA_FABRIC_IMAGE` 是 tag@sha256，Compose 使用 release overlay 从清单的固定引用拉取；若是 `data-fabric:local` 本地候选，则不加 release overlay，并先确认该 image ID 与清单相符。两种方式都必须加 `docker-compose.recovery.yml`，它会将 MCP 端口绑定到回环地址。确认 Docker 实际 app 与 MySQL image ID、revision label 与备份清单相符。不要复用已有 Compose 项目名或数据库卷，也不要让旧版本应用连接已迁移数据库。回滚应继续隔离本恢复项目，从已校验备份恢复到另一个新项目。

## 验证允许与拒绝的任务

使用两个单独签发的 OIDC access token，tenant claim 按部署配置映射；两次调用使用同一来源身份和同一只读查询：

1. 与来源 owner 同 tenant 的允许用户，应能列出来源并执行一个已知安全的只读任务，例如部署指南中的示例 `orders` 查询。
2. 不同 tenant 的用户不应看到或查询该来源。客户端应得到产品文档定义的 not-found/access-denied 结果，不应返回行数据或内部详情。
3. 用 owner 身份对应的管理 token 对一个所选来源执行连接检查，以验证恢复后的加密凭据确实可解密且来源可达。
4. 确认来源身份、活动及历史策略版本、发布结果和预期审计记录。完整元数据库 dump 包含这些表；活动查询游标和处理中任务不在恢复范围内。
5. 记录负向验收：用错误 age identity 或损坏的归档执行 `verify` 必须失败；在一次性项目副本中移除仍被已存来源凭据引用的旧 key，启动必须 fail closed；不支持的 schema 来源不得被描述为已验证。
6. 对同一候选重复运行初始化和启动迁移，确认来源数量、key ID、策略历史和审计结果没有额外变化。MySQL DDL 中断、旧 schema 升级来源等超出当前已证明范围的情形，要保留为未验证。

记录 UTC 时间、候选 SHA、两种镜像身份、MySQL 版本、归档校验和、key ID、doctor 结果、来源连接结果，以及脱敏后的允许/拒绝结果。不要在公开 issue 附加 `.env`、age identity、token、SQL dump、原始来源响应或未脱敏日志。

## 一致性、保留与边界

- 工具在 dump 期间暂停当前 Compose 项目的 API/MCP 写入服务并在结束后重启原先运行的服务。MySQL `--single-transaction` 提供 InnoDB 一致性快照，但不能让 MySQL DDL 回滚，也不协调第二个应用实例。一个元数据库同一时间只运行一个应用版本。
- age 加密保护 `.env` 和元数据。将 identity 与归档分开放置，限制备份读取者，按恢复目标保留足够副本，并定期在另一主机上验证。保留期限应遵循部署自身的数据保留要求；项目不设通用期限。
- 保留所有仍被现存记录引用的历史密钥。丢失密钥不能从数据库 dump 恢复。密钥轮换需保留旧密钥，直至所选来源连接检查通过。
- 当前 profile 只覆盖 MySQL。其他元数据库、超大数据库、复制/故障切换、中断 DDL、旧升级来源、生产规模恢复时间、业务源数据和高可用都需要单独证据。
- 这是由操作者控制的恢复流程，不是自动回滚，也不构成生产支持或 SLA 承诺。新实例完成以上检查前，保留原部署不变。
