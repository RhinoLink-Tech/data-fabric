# SQLite 元数据实验原型

[English](sqlite-metadata-prototype.md) · [方案评估](single-node-metadata-evaluation-zh.md) · [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

这是探索单机部署减少独立 MySQL 元数据服务的源码实验，**不是生产部署、发布镜像或 WorkBuddy 对外服务**。默认仍为 MySQL，不会自动迁移现有数据；release 模式和副本路由会拒绝 SQLite。

在具有 C 编译器及已固定 Go 依赖的环境中构建：

```sh
CGO_ENABLED=1 go build -tags datafabric_sqlite,datafabric_core -o data-fabric ./cmd/server
```

开发配置设置 `DATABASE_DRIVER=sqlite`，并将 `DATABASE_SQLITE_PATH` 指向已有的、服务专用本地目录中的绝对文件路径。最终目录不能允许组或其他用户写入；已有数据库必须是仅属主可访问的普通文件。新文件以 0600 权限创建。认证和凭证加密仍需按项目配置；以上两项本身不是完整部署配置。不要使用网络文件系统或容器临时层；本地性和上级目录安全由运维负责。不要将实验原型开放到公网。

连接使用 WAL、FULL 同步、外键、五秒锁等待和 IMMEDIATE 事务，跨进程串行化写入。测试覆盖合成数据源凭证加密、策略版本及审计的重启持久化，以及两个进程竞争发布策略；尚未证明完整 REST/MCP 拓扑、故障恢复、备份恢复、MySQL 迁移、所有文件系统及负载下的正确性。运行中不能只复制数据库主文件作为备份。

现有发布 Dockerfile 禁用 CGO，不能使用本原型。缺少实验编译标签或 CGO 时会明确拒绝 SQLite，不会静默更换存储。后续验收、运维设计及测量见 #444。
