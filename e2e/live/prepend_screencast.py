#!/usr/bin/env python
"""对着**线上**抓前插前后的真实合成帧，逐帧比对找「全屏闪烁」。

和 `prepend_flicker.py` 的分工：那个量 DOM 覆盖率（结构性空白），这个看**像素**
（底色/主题色闪、内容整体位移都算）。两者都通过才算「不闪」。

    .venv/bin/python e2e/live/prepend_screencast.py [--headed] [--session <key>]

只读：只发 GET，不发消息、不重启、不改配置。
"""

from __future__ import annotations

import argparse
import base64
import io
import os
import shutil
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import harness  # noqa: E402
from PIL import Image, ImageChops, ImageStat  # noqa: E402

OUT = "/tmp/live-frames"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="")
    parser.add_argument("--root", default="mindfs")
    parser.add_argument("--session", default="")
    parser.add_argument("--min-exchanges", type=int, default=24)
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--throttle", type=int, default=1, help="CPU 降速倍数（撑开提交与重渲染之间的窗口）")
    parser.add_argument("--wheel", action="store_true", help="用真实滚轮手势而不是直接写 scrollTop")
    args = parser.parse_args()

    env = harness.load_env()
    if args.url:
        env["MINDFS_LIVE_URL"] = args.url.rstrip("/")
    print("产物：", harness.dump(harness.bundle_hashes(env)))

    from playwright.sync_api import sync_playwright
    from scroll_repro import pick_long_session

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=not args.headed)
        page = browser.new_page(viewport={"width": 1100, "height": 780})
        preps: list[int] = []
        page.on("request", lambda req: preps.append(1) if "before_seq=" in req.url else None)

        harness.login(page, env)
        target, root = args.session, args.root
        if not target:
            picked = pick_long_session(page, env, root, args.min_exchanges)
            if not picked:
                print("× 没有够长的会话")
                browser.close()
                return 2
            target = picked["key"]
            print(f"会话 {target}（{picked['total']} 条历史）")

        harness.open_session(page, env, root, target)
        page.wait_for_timeout(1500)
        if not page.evaluate("window.__liveProbe.metrics().virtualized"):
            print("× 该会话没走虚拟化")
            browser.close()
            return 3
        rect = page.evaluate("""() => {
          const el = window.__liveProbe.scroller();
          const r = el.getBoundingClientRect();
          return {top: r.top, height: r.height, left: r.left, width: r.width};
        }""")

        client = page.context.new_cdp_session(page)
        frames: list[dict] = []
        client.on("Page.screencastFrame",
                  lambda p: (frames.append(p), client.send("Page.screencastFrameAck", {"sessionId": p["sessionId"]})))
        client.send("Page.startScreencast", {"format": "png", "everyNthFrame": 1})
        page.wait_for_timeout(300)

        if args.throttle > 1:
            page.context.new_cdp_session(page).send("Emulation.setCPUThrottlingRate", {"rate": args.throttle})
        centre = page.evaluate("""() => {
          const el = window.__liveProbe.scroller();
          const r = el.getBoundingClientRect();
          return {x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2)};
        }""")
        page.mouse.move(centre["x"], centre["y"])
        mark = len(preps)
        markFrames = len(frames)
        for _ in range(200):
            if args.wheel:
                page.mouse.wheel(0, -120)
            else:
                page.evaluate("""() => {
                  const el = window.__liveProbe.scroller();
                  el.scrollTop = Math.max(0, el.scrollTop - Math.round(el.clientHeight * 0.25));
                }""")
            page.wait_for_timeout(120)
            if len(preps) > mark:
                break
        print(f"前插请求：{len(preps) - mark}")
        page.wait_for_timeout(2500)
        client.send("Page.stopScreencast")
        browser.close()

    print(f"帧：{len(frames)}（前插前 {markFrames}）")
    shutil.rmtree(OUT, ignore_errors=True)
    os.makedirs(OUT)
    prev = None
    rows = []
    for i, f in enumerate(frames):
        img = Image.open(io.BytesIO(base64.b64decode(f["data"]))).convert("RGB")
        crop = img.crop((int(rect["left"]), int(rect["top"]) + 2,
                         int(rect["left"] + rect["width"]), int(rect["top"] + rect["height"]) - 2)).convert("L")
        stat = ImageStat.Stat(crop)
        diff = None
        if prev is not None and prev.size == crop.size:
            diff = round(ImageStat.Stat(ImageChops.difference(prev, crop)).mean[0], 2)
        rows.append((i, round(stat.mean[0], 1), round(stat.stddev[0], 2), diff))
        if i >= markFrames - 2:
            open(f"{OUT}/f{i:03d}.png", "wb").write(base64.b64decode(f["data"]))
        prev = crop

    print("frame | mean | stddev(小=空白) | diff-prev")
    worst = sorted([r for r in rows if r[3] is not None], key=lambda r: -r[3])[:8]
    for r in rows:
        if r[0] >= markFrames - 2:
            print(r, " <-- 前插" if r[0] >= markFrames else "")
    print("最大帧差：", worst)
    blanks = [r for r in rows if r[2] < 5]
    print(f"低方差帧（疑似空白）：{blanks[:6] or '无'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
