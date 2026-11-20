# Responses 脱敏夹具

`request_two_turns.json` 取自一次真实 Codex 请求（`/tmp/codex-probe/requests/002.json`，
2026-11-20 在本机用隔离 `CODEX_HOME` 抓的 `/v1/responses` body），只保留折叠需要的结构。

脱敏规则：

- 保留 `model`、`input` 数组顺序与每一项的 `type`。
- `reasoning.encrypted_content` 置空、`summary` 置空（加密推理内容与折叠无关，且不可读）。
- `message.content[].text` 全部换成 `user-N` / `developer-N` 占位；真实内容含用户提示词与
  Codex 的开发者指令，不进仓库。
- `custom_tool_call.input` 截断到 200 字节（原样是 Codex 生成的 JS）。
- 两个工具输出的正文都换成 30000 个 `A`：真实输出是命令回显，且长度对折叠判定（rail、
  每块节省）才是关键。
- 删除 `additional_tools`（24 KB 的工具 schema，与折叠无关）。
- 第二个轮次是**构造**的：抓包里 Codex 只用了 custom tool，`function_call` /
  `function_call_output` 按 Responses API 的公开形状补齐，用来钉住另一种输出形态
  （`output` 为字符串字面量）。

可以进仓库：没有用户内容、没有凭据、没有本机路径（唯一路径是 `/tmp/work` 占位）。
