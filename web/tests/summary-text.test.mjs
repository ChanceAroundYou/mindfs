import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

import { summaryPlainText } from "../src/shared/summaryText.ts";

const read = (relative) => fs.readFileSync(path.resolve(import.meta.dirname, "../src", relative), "utf8");

// S8：卡片按 pre-wrap 渲染、没有 Markdown 解析器，`**结论**` 与反引号原样显示
// 就是一串符号。四个标签行必须剥成正文，行与行之间也要保持分开。
test("summaryPlainText strips the label-line markdown but keeps the lines", () => {
  const raw = [
    "**结论**：卡片摘要改成纯文本渲染，存量数据不用改库。",
    "**证据**：`npx vitest run` 968 通过；改动 `TaskCardRows.tsx`。",
    "- 第一项 `go test ./...` 全绿",
    "- 第二项 tsc 干净",
    "**没做或未验证**：真机点验。",
    "**下一步**：无",
  ].join("\n");
  const got = summaryPlainText(raw);
  assert.equal(
    got,
    [
      "结论：卡片摘要改成纯文本渲染，存量数据不用改库。",
      "证据：npx vitest run 968 通过；改动 TaskCardRows.tsx。",
      "第一项 go test ./... 全绿",
      "第二项 tsc 干净",
      "没做或未验证：真机点验。",
      "下一步：无",
    ].join("\n"),
  );
});

// 表格是 #103 那条 2,833 字卡片的主要噪声：分隔行整行丢掉，内容行的竖线换成空格，
// 否则三行 clamp 里全是 `| --- |`。
test("summaryPlainText flattens tables and drops their separator rows", () => {
  const raw = ["| 项 | 状态 |", "| --- | :-: |", "| 构建 | 通过 |"].join("\n");
  assert.equal(summaryPlainText(raw), "项 状态\n构建 通过");
});

// 代码里的 shell 管道不是表格（行首尾都没有竖线），必须原样保留。
test("summaryPlainText leaves inline shell pipes alone", () => {
  assert.equal(summaryPlainText("跑 `git log | head -5` 看提交"), "跑 git log | head -5 看提交");
});

// 围栏块与分隔线：线下没有正文，卡片三行里出现 ```` ``` ```` 与 `---` 纯属浪费。
test("summaryPlainText drops fences and rule lines, links keep their text", () => {
  const raw = ["```bash", "make test", "```", "---", "见 [计划](https://example.com/p)，见 ![图](a.png)。"].join("\n");
  assert.equal(summaryPlainText(raw), "make test\n见 计划，见 图。");
});

// 存量 #103 那条：标题、引用、多级缩进混杂时也只留文字，且不能出现零宽残渣。
test("summaryPlainText handles long legacy summaries without leaving markers", () => {
  const raw = ["## 结论", "> 引用一句", "  * 缩进行", "**粗体** 与 __下划线__ 与 ~~删除~~"].join("\n");
  const got = summaryPlainText(raw);
  assert.equal(got, "结论\n引用一句\n缩进行\n粗体 与 下划线 与 删除");
  for (const marker of ["**", "__", "~~", "`", "##", "> "]) {
    assert.ok(!got.includes(marker), `plain text still contains ${marker}: ${got}`);
  }
});

// 卡片用同一份纯文本做正文与 title：悬停看到的不能还是带 Markdown 的原文。
test("TaskCardRows renders the plain summary in both the body and the title", () => {
  const source = read("components/task/TaskCardRows.tsx");
  assert.ok(source.includes('import { summaryPlainText } from "../../shared/summaryText";'));
  assert.ok(source.includes("summaryPlainText(task.result_summary)"));
  assert.ok(source.includes("title={resultSummary}"));
  assert.ok(!source.includes("title={task.result_summary}"));
  const body = source.slice(source.indexOf('data-testid="task-card-result"'));
  assert.ok(body.slice(0, 400).includes("{resultSummary}"), "card body must render the plain summary");
});