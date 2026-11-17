#!/usr/bin/env python
"""复制聊天正文时的选区范围 —— 对着**线上**跑（也可指到任意实例做 A/B）。

现象（2026-11-17 用户报）：长会话里向下滚动后再拖选正文，选区会沿着自动滚动一路
扩到会话标题栏：标题、header 和上方全部正文一起被选中，复制出来是整篇。

判据（拖选手势之后、不再有任何输入）：

    escaped == False  且  containsAllAbove == False   → 正常：选区停在正文里
    escaped == True                                  → 失控：标题/header 进了选区

只读：只发 GET、只做鼠标拖选，不发消息、不重启、不改配置。

    .venv/bin/python e2e/live/selection_scope.py
    ... --url http://127.0.0.1:7431/mindfs          # 换实例（隔离实例验修复）
    ... --bundle web/dist/assets/index-xxxx.js      # 用本地新产物顶替线上 bundle
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import harness  # noqa: E402


def pick_long_session(page, env: dict[str, str], root: str, min_exchanges: int) -> dict | None:
    for item in harness.sessions(page, env, root, limit=30):
        if item.get("parent_session_key"):
            continue
        total = harness.session_total(page, env, root, item["key"])
        if total >= min_exchanges:
            return {**item, "total": total}
    return None


PROBE_JS = r"""
() => {
  const content = document.querySelector('[data-mindfs-session-content-width]');
  const scroller = content ? content.parentElement : null;
  // 会话标题在 SessionViewerHeader 的 h1 里；页面里还有侧栏 header，别拿错。
  const title = document.querySelector('header h1');
  const box = scroller ? scroller.getBoundingClientRect() : null;
  // 起点必须落在**正文文本**上：直接按几何比例取点会偶尔落在卡片按钮/图标上，
  // 拖选根本起不来（实测第二次跑出空选区）。用 caretRangeFromPoint 扫一个文本点。
  let start = null;
  if (box) {
    for (let frac = 0.7; frac >= 0.2 && !start; frac -= 0.05) {
      const y = box.top + box.height * frac;
      for (const dx of [120, 200, 60, 280]) {
        const x = box.left + dx;
        const caret = document.caretRangeFromPoint(x, y);
        const node = caret && caret.startContainer;
        if (!node || node.nodeType !== 3) continue;
        if (!node.data || node.data.trim().length < 20) continue;
        if (!content.contains(node)) continue;
        start = { x, y };
        break;
      }
    }
  }
  return {
    hasContent: !!content,
    headerText: title ? title.innerText.trim() : '',
    box: box ? { left: box.left, top: box.top, width: box.width, height: box.height } : null,
    start,
    scrollTop: scroller ? scroller.scrollTop : 0,
    scrollHeight: scroller ? scroller.scrollHeight : 0,
  };
}
"""

READ_JS = r"""
() => {
  const sel = window.getSelection();
  const text = sel ? sel.toString() : '';
  const content = document.querySelector('[data-mindfs-session-content-width]');
  const title = document.querySelector('header h1 span:last-child');
  const titleText = title ? title.innerText.trim() : '';
  let escapesContent = false;
  let startOutside = null;
  let endOutside = null;
  if (sel && sel.rangeCount > 0 && content) {
    const range = sel.getRangeAt(0);
    escapesContent = !content.contains(range.commonAncestorContainer);
    startOutside = !content.contains(range.startContainer);
    endOutside = !content.contains(range.endContainer);
  }
  return {
    length: text.length,
    escaped: titleText ? text.includes(titleText) : false,
    escapesContent,
    startOutside,
    endOutside,
    head: text.slice(0, 30),
    tail: text.slice(-30),
  };
}
"""


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="", help="覆盖 .env 里的 MINDFS_LIVE_URL")
    parser.add_argument("--root", default="mindfs", help="项目 id（默认 mindfs）")
    parser.add_argument("--session", default="", help="指定会话 key（默认自动挑一条长的）")
    parser.add_argument("--min-exchanges", type=int, default=10, help="自动挑会话时的最小历史条数")
    parser.add_argument("--bundle", default="", help="用这份 index-*.js 顶替线上产物（A/B 用）")
    parser.add_argument("--headed", action="store_true")
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    print("产物：", harness.dump(harness.bundle_hashes(env)))

    from playwright.sync_api import sync_playwright

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=not args.headed)
        # 一次性探针，不要 SW：线上注册的 service worker 会用缓存的旧 bundle 顶掉
        # --bundle 注入的产物，A/B 就变成「新旧都是旧的」（实测 stamp 不匹配）。
        page = browser.new_context(service_workers="block", viewport={"width": 1280, "height": 900}).new_page()
        errors: list[str] = []
        page.on("pageerror", lambda e: errors.append(str(e)))
        if args.bundle:
            served = harness.bundle_hashes(env).get("served") or ""
            body = Path(args.bundle).read_text(encoding="utf-8", errors="replace")
            page.route(
                f"**/assets/{served}",
                lambda route: route.fulfill(status=200, content_type="application/javascript", body=body),
            )
            print(f"! 已用 {Path(args.bundle).name} 顶替线上 bundle —— 这是 A/B，不是线上真相")

        harness.login(page, env)
        target, root = args.session, args.root
        if not target:
            picked = pick_long_session(page, env, root, args.min_exchanges)
            if not picked:
                print(f"× {root} 下没有 ≥{args.min_exchanges} 条的会话")
                browser.close()
                return 2
            target = picked["key"]
            print(f"会话 {target}（{picked['total']} 条历史，{picked.get('name') or '未命名'}）")

        harness.open_session(page, env, root, target)
        page.wait_for_timeout(1200)
        info = page.evaluate(PROBE_JS)
        if not info["hasContent"]:
            print("× 页面里没有会话正文容器")
            browser.close()
            return 2
        print("header：", repr(info["headerText"][:40]))

        # 从正文中点开始拖选，向上拖到标题栏位置（容器上方 20px）。不预先滚动：
        # 会话打开就停在最新一屏，往上拖时的浏览器自动滚动负责把上方正文卷进来。
        box = info["box"]
        start = info.get("start")
        if not start:
            print("× 视口里没找到可拖选的正文文本")
            browser.close()
            return 2
        page.mouse.move(start["x"], start["y"])
        page.mouse.down()
        page.mouse.move(start["x"], max(2, box["top"] - 20), steps=20)
        # 按住不放，让浏览器的拖选自动滚动把上方历史卷进选区
        page.wait_for_timeout(1500)
        page.mouse.up()
        page.wait_for_timeout(200)

        result = page.evaluate(READ_JS)
        browser.close()

    print("拖选结果：", json.dumps(result, ensure_ascii=False))
    if errors:
        print("页面错误：", errors[:3])

    problems = []
    if result["startOutside"] or result["endOutside"]:
        problems.append("选区有一端落在正文容器外（拖到标题栏/其它界面去了）")
    if result["escaped"]:
        problems.append("会话标题进了选区")
    if result["length"] == 0:
        problems.append("选区是空的（拖选被脚本打断）")
    if not problems:
        print("✓ 正常：选区停在聊天正文里")
        return 0
    for line in problems:
        print("×", line)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())