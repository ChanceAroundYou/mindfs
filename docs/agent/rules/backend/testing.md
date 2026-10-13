# 后端测试规范（长期规则）

- 位置：与被测文件同目录 `xxx_test.go`；契约测试（如 `workspace_test.go` 的共享实例/分区断言）
  是防退化的另一半，删测试即删约束。
- 命令：`make test`（Go 全量）。改 `ws.go` handler 后必跑对应包测试 + 手查 goroutine 派发。
- 新增 handler/usecase 必须同批加测试；`role_test.go`（控制面表）、`workspace_test.go`
  （账户分区）、`startup_config_test.go`（启动配置）三类契约测试不许降级为跳过。

```bash
make test                 # Go 全量
go test ./server/internal/api/...   # 单包
```
