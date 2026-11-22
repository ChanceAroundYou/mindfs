"""右侧多项目会话列表：后台会话跑完后时间必须及时更新（2026-11-22 回归）。

现场（本机节点、llmux 项目「修复」会话）：11:47 跑完，11:52 列表仍显示开跑前的
「49m」，之后一直不动。根因链条（只读取证，见 web/src/shared/pendingCompletion.ts）：

  1. `session.stream` / `session.done` 只发给**挂载了该会话**的客户端
     （StreamHub 按 sessionKey 绑客户端）—— 右侧列表里后台项目的会话没挂载，收不到；
  2. 那时唯一的完成信号是 5s `/api/replying-sessions` 轮询的 pending true→false
     差分，而判定提示音/未读的那份差分**故意排除激活节点**（激活节点假定 WS 已处理）；
  3. 轮询只用差分去灭灯，从不重拉列表 → 卡片时间永久停在开跑前那条 `updated_at`。

本用例完全用路由拦截造出这两份真相（会话列表接口 + 旁路回复状态接口），不依赖
真实 agent，也不碰本机 7331：

  1. 会话列表只返回一条「49 分钟前更新」的会话 → 卡片显示 49m；
  2. 旁路状态变成 replying → 卡片亮灯（证明轮询确实在看这条会话）；
  3. 旁路状态回到不回复 + 列表接口改成「刚刚更新」→ 修复后 5s 轮询内卡片变成
     「刚刚」；修复前（去掉轮询里的列表重拉）会一直停在 49m。

跑法（隔离实例，与其它 e2e 用例一致）：

    .venv/bin/python -m pytest e2e/test_session_list_time_refresh.py -v -p no:cacheprovider
"""

from __future__ import annotations

import json
import re
import time
import urllib.parse
from datetime import datetime, timezone

from playwright.sync_api import Page, expect

SESSION_KEY = "1795316462-0592cf467797"
SESSION_NAME = "修复"
# 49 分 5 秒前：在 formatTime 的同一档（floor → 49m），留 55s 余量给测试自身耗时
OLD_SECONDS_AGO = 49 * 60 + 5

JUST_NOW = re.compile(r"Just now|刚刚")


def _iso(seconds_ago: float = 0) -> str:
    stamp = datetime.fromtimestamp(time.time() - seconds_ago, tz=timezone.utc)
    return stamp.isoformat().replace("+00:00", "Z")


def _is_sessions_list(url: str) -> bool:
    parts = urllib.parse.urlsplit(str(url))
    return "/api/sessions" in parts.path and "multi_root=1" in parts.query


def _is_replying(url: str) -> bool:
    return "/api/replying-sessions" in urllib.parse.urlsplit(str(url)).path


def test_background_session_completion_refreshes_list_time(authed_page: Page, project: dict) -> None:
    page = authed_page
    root_id = str(project["id"])
    created_at = _iso(2 * 3600)
    state = {
        "updated_at": _iso(OLD_SECONDS_AGO),
        "replying": False,
        "list_hits": 0,
    }

    def sessions_handler(route) -> None:
        state["list_hits"] += 1
        item = {
            "key": SESSION_KEY,
            "name": SESSION_NAME,
            "root_id": root_id,
            "updated_at": state["updated_at"],
            "created_at": created_at,
            "type": "chat",
            "agent": "claude",
            "model": "sonnet",
        }
        route.fulfill(
            status=200,
            content_type="application/json",
            body=json.dumps(
                {
                    "groups": [
                        {
                            "rootId": root_id,
                            "rootName": "llmux",
                            "latestSessionTime": state["updated_at"],
                            "totalCount": 1,
                            "pinnedKeys": [],
                            "pinnedItems": [],
                            "items": [item],
                        }
                    ]
                }
            ),
        )

    def replying_handler(route) -> None:
        items = []
        if state["replying"]:
            items.append(
                {"rootId": root_id, "sessionKey": SESSION_KEY, "status": "replying"}
            )
        route.fulfill(
            status=200,
            content_type="application/json",
            body=json.dumps({"sessions": items}),
        )

    # 卡片右侧信息列（时间 / 蓝灯）：名字按钮 → 上一级是名字+图标那格，
    # 它的**兄弟**节点才是右侧列（`min-width: 24px; justify-content: flex-end`）。
    def time_column():
        return page.locator(f'button:has(span[title="{SESSION_NAME}"])').first.locator(
            "xpath=../following-sibling::div[1]"
        )

    page.route(_is_sessions_list, sessions_handler)
    page.route(_is_replying, replying_handler)
    try:
        page.reload(wait_until="domcontentloaded")
        page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)
        try:
            page.click('button:has-text("Skip tour")', timeout=2_000)
        except Exception:  # noqa: BLE001 - 引导浮层只在首次出现
            pass

        column = time_column()
        # 首屏偶尔会撞上「受保护 API 还没就绪」的空列表（既有启动竞态，与本用例无关），
        # 那时没人会重拉会话列表、卡片永远不出现 —— 重载兜最后一次。
        for attempt in range(3):
            try:
                column.wait_for(state="visible", timeout=10_000)
                break
            except Exception:  # noqa: BLE001 - 仅在卡片缺席时重载
                if attempt == 2:
                    raise
                page.reload(wait_until="domcontentloaded")
                page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)

        # 1. 开跑前：列表给出旧 updated_at，卡片按它算出「49m」
        expect(column).to_have_text("49m", timeout=30_000)
        assert state["list_hits"] >= 1, "会话列表接口没有被请求过"

        # 2. 开跑：旁路状态变 replying，卡片亮灯（证明轮询确实在看这条后台会话）
        state["replying"] = True
        expect(
            column.locator('[aria-label="Replying"], [aria-label="正在回复"]')
        ).to_have_count(1, timeout=20_000)

        # 3. 跑完：服务端 updated_at 已触摸、旁路状态消失 —— 列表必须重拉，时间跟着变
        hits_before = state["list_hits"]
        state["updated_at"] = _iso(0)
        state["replying"] = False
        expect(column).to_have_text(JUST_NOW, timeout=20_000)
        assert state["list_hits"] > hits_before, (
            "时间更新必须来自一次新的列表拉取（不是原地重算旧值）"
        )
    finally:
        page.unroute(_is_sessions_list, sessions_handler)
        page.unroute(_is_replying, replying_handler)
