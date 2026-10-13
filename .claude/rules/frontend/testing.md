# 测试规范（长期规则）

- 现状：前端 `web/tests/` 108 文件 / 98 个 `*.test.mjs`，`node --test`
  （`package.json` scripts.test），已有 `frontend-split.test.mjs`；
  后端 `server/` 下 `*_test.go`，`make test`。
- **新复用件必须带测试**：新进 `shared/` 的函数、新通用组件，同批加 `web/tests/*.test.mjs`；
  存量补测见 `TODO.md`。
- **前端测试栈切 Vitest**：新测试按 Vitest 写，老测试逐步迁。独立 TODO（引依赖 + 改链路，不顺手做）。
- **命名规范测试**：新建 `web/tests/naming-convention.test.mjs`，扫描 `src/` 校验
  文件名 = 主 export 名、`.ts` 无 JSX、Utils/Helpers 后缀语义。

## 测试命令

```bash
cd web && npm test        # 前端：node --test tests/*.test.mjs
make test                 # 后端：Go 测试
```
