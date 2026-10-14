# 测试规范（长期规则）

- 前端测试统一由 Vitest 运行，支持 `.test.ts` 与 `.test.mjs`；`npm test` 是唯一前端测试入口。
  不保留 Node `node:test` 兼容 lane 或独立的第二测试运行器。
- **新复用件必须带测试**：新进 `shared/` 的函数、新通用组件，同批加入 `web/tests/` 中的 Vitest 测试；
  存量补测见 `TODO.md`。
- **命名规范测试**：`web/tests/naming-convention.test.mjs` 扫描 `src/` 校验
  文件名 = 主 export 名、`.ts` 无 JSX、Utils/Helpers 后缀语义。
- 后端测试放在 `server/` 对应包的 `*_test.go`，通过 `make test` 运行。

## 测试命令

```bash
cd web && npm test        # 前端 Vitest 测试
make test                 # 后端 Go 测试
```
