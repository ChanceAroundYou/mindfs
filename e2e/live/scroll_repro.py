#!/usr/bin/env python
"""一次上滑是否连翻多页 —— 对着**线上**跑（也可指到任意实例做 A/B）。

背景（2026-10-27）：`web/tests/session-virtualization.tsx` 的 `runPagingChecks` 每轮
自己写 `scrollTop = 0`，等于替用户反复触发，所以它从来抓不到「一次上滑连翻到底」。
这个脚本补的就是那一条：**只滚一次，然后完全不操作**，看后续自己又翻了几页。

    ~/.hermes/hermes-agent/.venv/bin/python e2e/live/scroll_repro.py
    ... --url http://127.0.0.1:7431/mindfs         # 换实例
    ... --session <key> --root <root>              # 指定会话
    ... --bundle <旧 index-*.js>                   # 用旧产物跑同一段（变异验证）

判断口径（一次手势之后、不再有任何输入）：

    prepends == 1  且  锚行没动   → 正常：只翻一页，视口回到原位
    prepends > 1                  → 失控：哨兵连着点下一页
    top == 0                      → 卡在顶部：回补一个字节都没写

只读：只发 GET，不发消息、不重启、不改配置。
"""

from __future__ import annotations

import argparse
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import harness  # noqa: E402

SETTLE_MS = 2500


def pick_long_session(page, env: dict[str, str], root: str, min_exchanges: int) -> dict | None:
    """挑一条历史够长的会话 —— 太短的会话一页就到底，复现不出连翻。"""
    for item in harness.sessions(page, env, root, limit=30):
        if item.get("parent_session_key"):
            continue
        total = harness.session_total(page, env, root, item["key"])
        if total >= min_exchanges:
            return {**item, "total": total}
    return None


def measure(page, env, root: str, key: str, settle_ms: int) -> dict:
    """一次上滑 + 静置，返回观测。**全程只滚一次**，其余靠等。"""
    prepends: list[float] = []
    page.on("request", lambda req: prepends.append(time.time()) if "before_seq=" in req.url else None)

    harness.open_session(page, env, root, key)
    page.wait_for_timeout(1200)
    before = page.evaluate("window.__liveProbe.metrics()")
    if not before.get("virtualized"):
        return {"skipped": "该会话没走虚拟化（条目数不到 48），连翻路径走不到"}

    # 计数必须在滚之前就开：哨兵在 scrollTop 落地的同一两帧内就会触发 loadMore，
    # 等锚行再开计数会把这次请求漏掉（实测漏成 0，误报「口径可能过期」）。
    mark = len(prepends)
    page.evaluate("window.__liveProbe.toTop()")
    # 锚行取「回补还没落地时视口最上面那几行」。采样太晚（>150ms）会读到回补后的位置，
    # 太早会读到虚拟器换窗口之前的旧行 —— 60ms 是实测两者之间。
    page.wait_for_timeout(60)
    anchor = page.evaluate("window.__liveProbe.anchors()")
    page.wait_for_timeout(max(0, settle_ms - 60))
    fired = len(prepends) - mark

    # 锚行在收敛中会短暂卸载（虚拟器先卸后挂），单点采样会读到 unmeasured 而误判。
    drift = "unmeasured"
    for _ in range(10):
        drift = page.evaluate("saved => window.__liveProbe.anchorDrift(saved)", anchor)
        if isinstance(drift, int):
            break
        page.wait_for_timeout(200)

    return {"before": before, "after": page.evaluate("window.__liveProbe.metrics()"), "fired": fired, "drift": drift}


def verdicts(result: dict) -> list[str]:
    if result.get("skipped"):
        return [result["skipped"]]
    before, after, fired, drift = result["before"], result["after"], result["fired"], result["drift"]
    problems = []
    if fired > 1:
        problems.append(f"失控：一次上滑触发了 {fired} 次前插请求")
    if fired == 0 and after["height"] > before["height"]:
        problems.append("前插发生了但没记到请求（口径可能过期）")
    if after["top"] == 0:
        problems.append("卡在顶部：回补一个字节都没写")
    # 锚行还在就必须回到原位：scrollTop 变大是正常的（上面插了一整页），行不能动。
    if isinstance(drift, int) and abs(drift) > 40:
        problems.append(f"位置没回原位：锚行漂移 {drift}px（上限 40 = 一行）")
    return problems


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="", help="覆盖 .env 里的 MINDFS_LIVE_URL")
    parser.add_argument("--root", default="mindfs", help="项目 id（默认 mindfs）")
    parser.add_argument("--session", default="", help="指定会话 key（默认自动挑一条长的）")
    parser.add_argument("--min-exchanges", type=int, default=24, help="自动挑会话时的最小历史条数")
    parser.add_argument("--bundle", default="", help="用这份 index-*.js 顶替线上产物（变异验证用）")
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--settle-ms", type=int, default=SETTLE_MS)
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    # 先排掉「线上还是旧产物」这一层：它长得和「代码本身没修好」一模一样。
    print("产物：", harness.dump(harness.bundle_hashes(env)))
    if args.bundle:
        print(f"! 已用 {Path(args.bundle).name} 顶替线上 bundle —— 这是变异验证，不是线上真相")

    from playwright.sync_api import sync_playwright

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=not args.headed)
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        if args.bundle:
            served = harness.bundle_hashes(env).get("served") or ""
            body = Path(args.bundle).read_text(encoding="utf-8", errors="replace")
            page.route(
                f"**/assets/{served}",
                lambda route: route.fulfill(status=200, content_type="application/javascript", body=body),
            )

        harness.login(page, env)

        target, root = args.session, args.root
        if not target:
            picked = pick_long_session(page, env, root, args.min_exchanges)
            if not picked:
                print(f"× {root} 下没有 ≥{args.min_exchanges} 条的会话，复现不出连翻")
                browser.close()
                return 2
            target = picked["key"]
            print(f"会话 {target}（{picked['total']} 条历史，{picked.get('name') or '未命名'}）")

        result = measure(page, env, root, target, args.settle_ms)
        browser.close()

    print("加载后：", harness.dump(result.get("before") or {}))
    print("一次上滑后（不再操作）：", harness.dump(result.get("after") or {}))
    if not result.get("skipped"):
        print(f"前插请求：{result['fired']}（期望 1）；锚行漂移：{result['drift']}")

    problems = verdicts(result)
    if not problems:
        print("✓ 正常：只翻一页，视口回到原位")
        return 0
    for line in problems:
        print("×", line)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
