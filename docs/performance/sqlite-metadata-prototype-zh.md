# SQLite 元数据实验原型

[English](sqlite-metadata-prototype.md) · [方案评估](single-node-metadata-evaluation-zh.md) · [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

这是探索单机部署减少独立 MySQL 元数据服务的源码实验，**不是生产部署、发布镜像或 WorkBuddy 对外服务**。默认仍为 MySQL，不会自动迁移现有数据；release 模式和副本路由会拒绝 SQLite。

在具有 C 编译器及已固定 Go 依赖的环境中构建：

```sh
CGO_ENABLED=1 go build -tags datafabric_sqlite,datafabric_core -o data-fabric ./cmd/server
```

开发配置设置 `DATABASE_DRIVER=sqlite`，并将 `DATABASE_SQLITE_PATH` 指向已有的、服务专用本地目录中的绝对文件路径。最终目录不能允许组或其他用户写入；已有数据库必须是仅属主可访问的普通文件。新文件以 0600 权限创建。认证和凭证加密仍需按项目配置；以上两项本身不是完整部署配置。不要使用网络文件系统或容器临时层；本地性和上级目录安全由运维负责。不要将实验原型开放到公网。

连接使用 WAL、FULL 同步、外键、五秒锁等待和 IMMEDIATE 事务，跨进程串行化写入。测试覆盖合成数据源凭证加密、策略版本及审计的重启持久化，以及两个进程竞争发布策略；尚未证明完整 REST/MCP 拓扑、所有故障恢复情况、MySQL 迁移、所有文件系统及负载下的正确性。新增合成测试覆盖一次写入中断回滚，以及在线快照恢复已提交的 WAL 数据、加密凭证、策略撤销和审计；这不构成完整的灾难恢复保证。

## 实验性备份与恢复

使用同一个启用 CGO 的实验版程序。目标目录必须事先存在、由服务专用，且组和其他用户不可写；备份与恢复目标必须是新路径，且不能有残留日志文件。命令不会覆盖已有文件。备份通过 SQLite `VACUUM INTO` 获取一致快照，可以在源服务运行时执行。

```sh
./data-fabric sqlite-metadata backup --source /srv/data-fabric/metadata.sqlite --output /srv/data-fabric/backups/metadata-20261120.sqlite
# 恢复前停止所有可能访问恢复目标的进程。
./data-fabric sqlite-metadata restore --source /srv/data-fabric/backups/metadata-20261120.sqlite --output /srv/data-fabric/restored.sqlite
```

若文件发布后同步失败，命令会报错并保留已创建的文件；重试前请检查并移除该新文件，命令不会覆盖它。输出文件仅属主可访问，但仍含敏感元数据，需妥善保护。凭证加密密钥不会包含在快照中，必须按现有密钥备份流程单独保存；恢复后必须使用正确的密钥环。先在隔离环境验证恢复，再将服务配置切换到新路径。该命令不会覆盖现有数据库，也不负责从 MySQL 迁移。运行中不能只复制数据库主文件作为备份。

现有发布 Dockerfile 禁用 CGO，不能使用本原型。缺少实验编译标签或 CGO 时会明确拒绝 SQLite，不会静默更换存储。后续验收、运维设计及测量见 #444。
