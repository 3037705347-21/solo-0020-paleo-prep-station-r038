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
- `GET /plans/{id}`
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`（批次方案可携带 `batch_stage_id` 引用共享阶段事实）
- `POST /specimens/{id}/release`
- `GET /prep-batches` 与 `POST /prep-batches`
- `GET /prep-batches/{id}`
- `POST /prep-batches/{id}/approval`
- `POST /prep-batches/{id}/stages`
- `POST /prep-batches/{id}/close`
- `POST /prep-batches/{id}/cancel`
- `POST /prep-batches/{id}/split`

## 多标本制备批次

负责人通过 `POST /prep-batches` 一次建立同一地点多份标本的制备批次：请求同时给出每份成员的标本资料和独立草稿方案，批次写入整体成功或整体失败。批次只共享阶段事实（`POST /prep-batches/{id}/stages`），每份标本继续保留独立方案、目标暴露比例、单调进度序列和终态；单份标本达到一百只稳定该标本，不会关闭批次，只有所有成员方案完成后显式 `close` 才会关闭。草稿批次可以整体 `cancel`（同时取消成员草稿方案），也可以把草稿成员 `split` 到新批次（原批次必须保留至少一份成员）。所有批次写入都走仓储互斥事务，并发阶段编号连续、同方案并发进度按单调规则得到确定结果。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow prepare-multi-specimen-batch
go run ./cmd/workflowcheck --workflow batch-concurrent-progress
go run ./cmd/workflowcheck --workflow batch-cancel-and-split
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
