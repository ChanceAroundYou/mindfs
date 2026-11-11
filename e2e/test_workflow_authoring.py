"""Agent-first Workflow authoring 的隔离实例浏览器验收。

假 Agent 只替换「生成提案」这一跳：用 Playwright 路由拦截 authoring 网关 POST，
返回一份合法的 typed patch。其余全部走真后端：subset validate/dry-run、CAS apply、
publish confirmation 都真实执行并落盘 —— 所以这些用例证明的是「未确认不写、
确认后才写」，而不是前端自说自话。

跑法：
    .venv/bin/python -m pytest e2e/test_workflow_authoring.py -v
"""

from __future__ import annotations

import json
import re
import time

from playwright.sync_api import Page, expect

VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
EDITOR_OPEN = '[data-onboarding="task-workflow-editor"]'
EDITOR = '[data-testid="workflow-draft-editor"]'
INSTRUCTION = '[data-testid="workflow-authoring-instruction"]'
GENERATE = '[data-testid="workflow-authoring-generate"]'
REVIEW = '[data-testid="workflow-proposal-review"]'
APPLY = '[data-testid="workflow-proposal-apply"]'
STATUS = '[data-testid="workflow-proposal-status"]'
ISSUES = '[data-testid="workflow-proposal-issues"]'
APPLIED = '[data-testid="workflow-proposal-applied"]'
GENERATION_ROUTE = re.compile(r"/api/workflows/[^/]+/authoring/proposals")


def _open_editor(page: Page):
    """切到 Board 并打开 Workflow drafts 面板；返回编辑器 locator。"""
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name="Board", exact=True).click()
    if page.locator(EDITOR).count():
        page.locator(EDITOR).get_by_role("button", name="Close", exact=True).click()
        page.wait_for_selector(EDITOR, state="detached", timeout=20_000)
    page.locator(EDITOR_OPEN).first.click()
    editor = page.locator(EDITOR)
    editor.wait_for(state="visible", timeout=20_000)
    return editor


def _configure_authoring_agent(page: Page, api_url: str, user_query: str) -> None:
    """生成入口复用会话命名的默认 agent；全新隔离账户里它是空的。"""
    resp = page.request.put(
        f"{api_url}/api/preferences/session-naming?{user_query}",
        data=json.dumps({"agent": "claude", "model": ""}),
        headers={"Content-Type": "application/json"},
    )
    assert resp.status == 200, resp.text()


def _selected_draft(editor) -> str:
    selected = editor.locator('nav button[aria-current="true"]')
    expect(selected).to_be_visible(timeout=20_000)
    return selected.inner_text()


def _draft_id(editor) -> str:
    return _selected_draft(editor).split(" · ")[0].strip()


def _revision(editor) -> str:
    return _selected_draft(editor).split(" · ")[-1].strip()


def _snapshot(page: Page, api_url: str, user_query: str, draft_id: str) -> dict:
    resp = page.request.get(f"{api_url}/api/workflows/{draft_id}/snapshot?{user_query}")
    assert resp.status == 200, resp.text()
    return resp.json()["draft"]


def _create_draft(editor) -> str:
    """点 New 并等到选中项真的换成新草稿（旧选中项也带 aria-current=true）。"""
    before = set(editor.locator("nav button").all_inner_texts())
    editor.get_by_role("button", name="New", exact=True).click()
    deadline = time.time() + 20
    while time.time() < deadline:
        selected = editor.locator('nav button[aria-current="true"]')
        if selected.count() and selected.inner_text() not in before:
            return selected.inner_text().split(" · ")[0].strip()
        time.sleep(0.1)
    raise AssertionError("New did not select a fresh draft")


def _fake_proposal(base: str, operations: list[dict], dry_run_issues: list[dict] | None = None) -> dict:
    diff = []
    for op in operations:
        node_id = (op.get("node") or {}).get("id") or op.get("node_id")
        entry = {"operation_id": op["id"], "op": op["op"]}
        if node_id:
            entry["node_id"] = node_id
        diff.append(entry)
    dry_run: dict = {"status": "waiting_user", "steps": []}
    if dry_run_issues:
        dry_run["issues"] = dry_run_issues
    return {
        "proposal": {
            "summary": "Add a notify step",
            "rationale": "Announce completion after the agent",
            "base_revision": base,
            "operations": operations,
        },
        "diff": {"operations": diff},
        "dry_run": dry_run,
        "issues": [],
    }


def _fulfill(payload: dict):
    return lambda route: route.fulfill(
        status=200, content_type="application/json", body=json.dumps(payload)
    )


def _poll_http(page: Page, url: str, want: int, timeout: float = 10.0):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = page.request.get(url)
        if last.status == want:
            return last
        time.sleep(0.2)
    assert last is not None and last.status == want, (
        f"{url} never returned {want}: {last.status if last else 'no response'}"
    )
    return last


ADD_NOTIFY = {
    "id": "op-node",
    "op": "add_node",
    "node": {"id": "notify", "name": "Notify", "kind": "notify", "params": {"message": "done"}},
}


def test_partial_accept_apply_and_publish(authed_page: Page, api_url: str, user_query: str) -> None:
    """接受 node、拒绝 edge → 真 CAS 落盘 → 重开看到同 revision → 单独确认才发布。"""
    page = authed_page
    _configure_authoring_agent(page, api_url, user_query)
    editor = _open_editor(page)
    draft_id = _create_draft(editor)
    assert _revision(editor) == "r1"

    operations = [ADD_NOTIFY, {"id": "op-edge", "op": "add_edge", "edge": {"from": "start", "to": "notify"}}]
    page.route(GENERATION_ROUTE, _fulfill(_fake_proposal("r1", operations)))
    try:
        editor.locator(INSTRUCTION).fill("add a notify step after the agent")
        editor.locator(GENERATE).click()
        expect(editor.locator(REVIEW)).to_be_visible(timeout=20_000)
        expect(editor.locator(STATUS)).to_have_attribute("data-status", "ready")

        # 逐项选择：只接受新节点，拒绝那条边。
        editor.locator('[data-operation-id="op-node"] input[type="checkbox"]').check()
        expect(editor.locator(APPLY)).to_be_enabled(timeout=10_000)
        editor.locator(APPLY).click()
        expect(editor.locator(APPLIED)).to_be_visible(timeout=20_000)
    finally:
        page.unroute(GENERATION_ROUTE)

    expect(editor.locator('nav button[aria-current="true"]')).to_contain_text("· r2", timeout=20_000)
    draft = _snapshot(page, api_url, user_query, draft_id)
    assert [n["id"] for n in draft["graph"]["nodes"]] == ["start", "notify"]
    assert not draft["graph"].get("edges"), "the rejected edge must not be persisted"

    # 重开编辑器：同一 revision 的图必须已经落盘（不是前端内存里的）。
    editor.get_by_role("button", name="Close", exact=True).click()
    page.wait_for_selector(EDITOR, state="detached", timeout=20_000)
    editor = _open_editor(page)
    editor.locator("nav button", has_text=draft_id).first.click()
    expect(editor.locator('nav button[aria-current="true"]')).to_contain_text("· r2", timeout=20_000)
    expect(editor.locator('.react-flow__node[data-id="notify"]')).to_be_visible(timeout=20_000)
    assert editor.locator(".react-flow__edge").count() == 0, "the rejected edge came back after reload"

    # 发布是独立动作：先弹确认，确认前没有不可变版本。
    editor.get_by_role("button", name="Publish", exact=True).click()
    dialog = page.get_by_role("dialog")
    expect(dialog).to_be_visible(timeout=10_000)
    assert page.request.get(f"{api_url}/api/workflows/versions/{draft_id}:v2?{user_query}").status == 404
    dialog.get_by_role("button", name="Confirm", exact=True).click()
    _poll_http(page, f"{api_url}/api/workflows/versions/{draft_id}:v2?{user_query}", 200)


def test_dry_run_issue_blocks_apply_and_persists_nothing(
    authed_page: Page, api_url: str, user_query: str
) -> None:
    """后端的 dry-run issue 是阻断信号，不是展示文案：apply 必须不可用且零写入。"""
    page = authed_page
    _configure_authoring_agent(page, api_url, user_query)
    editor = _open_editor(page)
    draft_id = _create_draft(editor)
    assert _revision(editor) == "r1"

    payload = _fake_proposal(
        "r1", [ADD_NOTIFY], dry_run_issues=[{"field": "graph", "message": "condition input missing"}]
    )
    page.route(GENERATION_ROUTE, _fulfill(payload))
    try:
        editor.locator(INSTRUCTION).fill("add a notify step")
        editor.locator(GENERATE).click()
        expect(editor.locator(REVIEW)).to_be_visible(timeout=20_000)
        expect(editor.locator(STATUS)).to_have_attribute("data-status", "invalid")
        expect(editor.locator(ISSUES)).to_contain_text("condition input missing")
        editor.locator('[data-operation-id="op-node"] input[type="checkbox"]').check()
        expect(editor.locator(APPLY)).to_be_disabled(timeout=10_000)
    finally:
        page.unroute(GENERATION_ROUTE)

    expect(editor.locator('nav button[aria-current="true"]')).to_contain_text("· r1")
    draft = _snapshot(page, api_url, user_query, draft_id)
    assert [n["id"] for n in draft["graph"]["nodes"]] == ["start"]


def test_stale_generation_adopts_server_revision(
    authed_page: Page, api_url: str, user_query: str
) -> None:
    """服务端已前进时，生成 409 必须采用服务端 revision 并重载，而不是继续用旧 base。"""
    page = authed_page
    _configure_authoring_agent(page, api_url, user_query)
    editor = _open_editor(page)
    draft_id = _create_draft(editor)
    assert _revision(editor) == "r1"

    # 另一个编辑者把同一份 Draft 推到 r2（UI 仍停在 r1）。
    draft = _snapshot(page, api_url, user_query, draft_id)
    resp = page.request.put(
        f"{api_url}/api/workflows/{draft_id}?{user_query}",
        data=json.dumps({"base_revision": "r1", "author": "user", "graph": draft["graph"]}),
        headers={"Content-Type": "application/json"},
    )
    assert resp.status == 200, resp.text()

    editor.locator(INSTRUCTION).fill("add a notify step")
    editor.locator(GENERATE).click()
    # 真实后端在调用 Agent 之前就返回 409，所以这条路径不需要 fake。
    expect(editor.locator(ISSUES)).to_contain_text("workflow changed on the server", timeout=20_000)
    expect(editor.locator('nav button[aria-current="true"]')).to_contain_text("· r2", timeout=20_000)
    expect(editor.locator(APPLY)).to_be_disabled(timeout=10_000)

    # 下一次生成本身就是证据：它必须带 adopted 的 r2，而不是旧 r1。
    seen: dict[str, str] = {}

    def fulfill(route):
        body = route.request.post_data_json or {}
        seen["base"] = body.get("base_revision", "")
        route.fulfill(
            status=200,
            content_type="application/json",
            body=json.dumps(_fake_proposal("r2", [ADD_NOTIFY])),
        )

    page.route(GENERATION_ROUTE, fulfill)
    try:
        editor.locator(GENERATE).click()
        expect(editor.locator(STATUS)).to_have_attribute("data-status", "ready", timeout=20_000)
        assert seen.get("base") == "r2", f"regeneration used a stale base: {seen}"
    finally:
        page.unroute(GENERATION_ROUTE)


def test_loop_inner_dag_saves_and_reopens(authed_page: Page, api_url: str, user_query: str) -> None:
    """Loop 容器里的内部 DAG 能编辑、保存、重开，并能在服务端 dry-run。"""
    page = authed_page
    loop_id = f"e2e-loop-{int(time.time())}"
    graph = {
        "nodes": [
            {
                "id": "loop",
                "name": "Loop",
                "kind": "loop",
                "params": {
                    "max_iterations": 2,
                    "exit_expression": "loop.output.done == true",
                    "graph": {
                        "nodes": [{"id": "inner", "name": "Inner", "kind": "agent", "params": {"prompt": "work"}}],
                        "edges": [],
                    },
                },
            }
        ],
        "edges": [],
    }
    created = page.request.post(
        f"{api_url}/api/workflows?{user_query}",
        data=json.dumps({"workflow_id": loop_id, "author": "user", "graph": graph}),
        headers={"Content-Type": "application/json"},
    )
    assert created.status == 200, created.text()

    editor = _open_editor(page)
    nav_item = editor.locator("nav button", has_text=loop_id).first
    nav_item.wait_for(state="visible", timeout=20_000)
    nav_item.click()
    expect(editor.locator('nav button[aria-current="true"]')).to_contain_text(loop_id)

    # 选中 Loop 节点 → 看到退出表达式 → 进入内部子图。
    loop_node = editor.locator('.react-flow__node[data-id="loop"]')
    loop_node.wait_for(state="visible", timeout=20_000)
    loop_node.click()
    expect(editor.locator('[data-testid="workflow-loop-exit"]')).to_have_value("loop.output.done == true")
    editor.locator('[data-testid="workflow-loop-enter"]').click()

    inner = editor.locator('[data-testid="workflow-loop-canvas"]')
    expect(inner).to_be_visible(timeout=10_000)
    inner_node = inner.locator('.react-flow__node[data-id="inner"]')
    inner_node.wait_for(state="visible", timeout=20_000)
    inner_node.click()
    inspector_name = inner.locator('[data-testid="workflow-loop-inspector"] input').first
    expect(inspector_name).to_have_value("Inner", timeout=10_000)
    inspector_name.fill("Inner renamed")
    inner.get_by_role("button", name="Back to top graph").click()
    expect(inner).to_have_count(0, timeout=10_000)

    # 保存进 Draft，随后重开必须看到改名后的内部节点。
    editor.get_by_role("button", name="Save", exact=True).click()
    expect(editor.locator('nav button[aria-current="true"]')).to_contain_text("· r2", timeout=20_000)
    editor.get_by_role("button", name="Close", exact=True).click()
    page.wait_for_selector(EDITOR, state="detached", timeout=20_000)

    editor = _open_editor(page)
    nav_item = editor.locator("nav button", has_text=loop_id).first
    nav_item.wait_for(state="visible", timeout=20_000)
    nav_item.click()
    editor.locator('.react-flow__node[data-id="loop"]').wait_for(state="visible", timeout=20_000)
    editor.locator('.react-flow__node[data-id="loop"]').click()
    editor.locator('[data-testid="workflow-loop-enter"]').click()
    inner = editor.locator('[data-testid="workflow-loop-canvas"]')
    inner.locator('.react-flow__node[data-id="inner"]').click()
    expect(inner.locator('[data-testid="workflow-loop-inspector"] input').first).to_have_value("Inner renamed")

    saved = _snapshot(page, api_url, user_query, loop_id)
    assert saved["revision"] == "r2"
    loop_params = saved["graph"]["nodes"][0]["params"]
    assert loop_params["max_iterations"] == 2
    assert loop_params["exit_expression"] == "loop.output.done == true"
    assert loop_params["graph"]["nodes"][0]["name"] == "Inner renamed"

    dry = page.request.post(
        f"{api_url}/api/workflows/dry-run?{user_query}",
        data=json.dumps({"graph": saved["graph"]}),
        headers={"Content-Type": "application/json"},
    )
    assert dry.status == 200, dry.text()


def test_mobile_view_is_read_only(authed_page: Page) -> None:
    """手机上保持可读，不要求复杂画布编辑：只读列表 + 生成入口禁用。"""
    page = authed_page
    editor = _open_editor(page)
    _create_draft(editor)
    try:
        page.set_viewport_size({"width": 390, "height": 844})
        expect(editor.locator('[data-testid="workflow-canvas-readonly"]')).to_be_visible(timeout=10_000)
        expect(editor.locator(GENERATE)).to_be_disabled(timeout=10_000)
    finally:
        page.set_viewport_size({"width": 1440, "height": 900})
        editor.get_by_role("button", name="Close", exact=True).click()
        page.wait_for_selector(EDITOR, state="detached", timeout=20_000)