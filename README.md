# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行四条业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界和业务流程。
- `internal/domain`：标本、方案、进度和放行记录的状态与不变量。
- `internal/repository`：互斥保护的内存仓储和原子提交。
- `internal/contracts`：稳定错误模型和公共请求结构。

## 构建与运行

```bash
go build ./...
go run ./cmd/paleo-prep-station --addr 127.0.0.1:18133
```

服务启动后，`GET /healthz` 返回就绪状态。

## 主要接口

- `GET /specimens` 与 `POST /specimens`
- `GET /specimens/{id}`
- `POST /specimens/{id}/plans`
- `GET /specimens/{id}/plans`
- `GET /plans/{id}`
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /plans/{id}/cancellation`
- `POST /specimens/{id}/release`
- `GET /batches` 与 `POST /batches`
- `GET /batches/{id}`
- `POST /batches/{id}/progress`
- `POST /batches/{id}/split`
- `POST /batches/{id}/cancellation`

多标本批次把同一地点的多份方案编入一次操作：批次内一次阶段写入共享同一阶段事实（序号、名称、说明、记录人），每份标本保留独立的比例、单调进度序列和方案/标本终态。批次写入是单一仓储事务，任一条目不合法则阶段事实和全部标本进度都不落库；单份标本完成不会提前关闭整批，只有全部成员终态批次才整体完成。批次支持拆分（源批至少保留一个成员）和取消（开放成员脱离后独立流转），成员归属与共享阶段历史永久保留。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow batch-shared-progress
go run ./cmd/workflowcheck --workflow batch-atomic-failure
go run ./cmd/workflowcheck --workflow batch-split
go run ./cmd/workflowcheck --workflow batch-cancel
go run ./cmd/workflowcheck --workflow batch-member-cancel
go run ./cmd/workflowcheck --workflow batch-concurrent
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
