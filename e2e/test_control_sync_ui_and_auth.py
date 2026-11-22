#!/usr/bin/env python3
"""T22 边缘页面与同步通道安全验收（真进程 + 真浏览器）。

两件事，一份脚本：

  安全 —— 同步通道与前端清单的鉴权：未启用同步的机器上端点根本不存在（404）、
           不带签名是 401、签名错是 401、nonce 重放还是 401。
           「无 token 404」之所以不是 401，是因为端点连注册都没有：token 缺省时
           这些路径在路由表里不存在，判据就是「装了但没开 = 看不见」。
  页面 —— 真 Chromium 打开边缘页面：在线横幅、断主节点后的本地模式横幅、
           两侧都改后的冲突横幅与冲突面板（点「保留本地」把裁决真的写回去）、
           节点不可达提示。顺带验一次前端镜像：把边缘安装目录的 index.html 移走，
           页面照旧出得来，证明它出的是镜像指针那一版。

跑法（与其它 e2e 一致，刻意不进常规门禁）：

    .venv/bin/python e2e/test_control_sync_ui_and_auth.py [--keep] [--binary PATH]

脚本自己 go build（不带部署前缀，页面就在 / 上）并用 VITE_MIND_FS_BASE=/ 现编一份
前端到临时目录 —— 随包的 web/dist 是 /mindfs 前缀，与无前缀后端配不上。
"""

from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import os
import secrets
import shutil
import sqlite3
import subprocess
import sys
import time
from datetime import datetime, timezone
import urllib.error
import urllib.request

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.dirname(E2E_DIR)
sys.path.insert(0, E2E_DIR)

import test_control_sync_offline as t21  # noqa: E402  复用双实例装置
from conftest import login  # noqa: E402  复用真实登录流程（含跳过引导）

from playwright.sync_api import sync_playwright  # noqa: E402

Fail = t21.Fail
check = t21.check
log = t21.log

PLAIN_PORT = 7472
DEAD_NODE_URL = "http://127.0.0.1:7498"  # 无人监听：用它制造「节点不可达」
DEAD_NODE_NAME = "Dead Node"
SYNC_TOKEN = "e2e-token-0123456789"  # 与 t21.prepare 写进 token_file 的值一致
PREF_KEY = t21.PREF_KEY
UI_VERSION = "e2e-1"
SERVER_BUILD = "v0.1.0-e2e"  # 二进制版本；镜像的 min_server_build 要落在它之下
BROWSER_USER = {"username": "e2e_sync", "password": "e2e-sync-7Kx9Qm2!", "role": "user"}


# ---------------------------------------------------------------- 构建产物


def build_binary(root):
    """现编一个二进制：不带部署前缀（页面在 / 上），但要带版本号。

    版本号不是装饰：边缘的前端镜像按 `min_server_build` 判定能不能激活，裸
    `go build` 得到的是 "dev"，任何 min_server_build 都比它新，镜像永远装不上。
    """
    binary = os.path.join(root, "mindfs")
    log("编译二进制（go build ./cli/cmd，不带部署前缀）")
    subprocess.run(["go", "build", "-ldflags", f"-X main.version={SERVER_BUILD}",
                    "-o", binary, "./cli/cmd"], cwd=REPO_ROOT, check=True)
    return binary


def build_dist(root):
    """现编一份 base=/ 的前端，并生成 ui-manifest.json。

    随包的 web/dist 是 /mindfs 前缀的构建，而这个脚本起的后端没有前缀 ——
    不重编的话页面在 / 上打不开，镜像也搬不过去。
    """
    dist = os.path.join(root, "dist")
    log("构建前端（VITE_MIND_FS_BASE=/）")
    env = dict(os.environ)
    env["VITE_MIND_FS_BASE"] = "/"
    subprocess.run(["npx", "vite", "build", "--outDir", dist, "--emptyOutDir"],
                   cwd=os.path.join(REPO_ROOT, "web"), env=env, check=True)
    manifest = os.path.join(dist, "ui-manifest.json")
    if not os.path.exists(manifest):
        log("生成 ui-manifest.json（server/cmd/ui-manifest）")
        subprocess.run(["go", "run", "./server/cmd/ui-manifest",
                        "-dist", dist,
                        "-build-version", UI_VERSION,
                        "-min-server-build", "v0.0.1"],
                       cwd=REPO_ROOT, check=True)
    return dist


class StaticDirInstance(t21.Instance):
    """带 MINDFS_STATIC_DIR 的实例。

    t21.Instance.start 会主动 unset 这个环境变量（它那套不涉及前端），
    而这里必须让每个实例出自己那一份安装目录 —— 后面要靠「移走边缘的安装副本、
    页面照旧出得来」证明镜像指针真的在生效。
    """

    def __init__(self, *args, static_dir="", **kwargs):
        super().__init__(*args, **kwargs)
        self.static_dir = static_dir

    def start(self):
        check(self.proc is None, f"{self.name} 已经在跑")
        if not t21.port_free(self.port):
            raise Fail(f"{self.name} 的端口 {self.port} 已被占用，先停掉遗留实例")
        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = self.config_dir
        if self.static_dir:
            env["MINDFS_STATIC_DIR"] = self.static_dir
        else:
            env.pop("MINDFS_STATIC_DIR", None)
        logf = open(self.log_path, "ab")
        self.proc = subprocess.Popen([self.binary, "--foreground"], env=env,
                                     stdout=logf, stderr=subprocess.STDOUT,
                                     start_new_session=True)
        log(f"{self.name} 启动 pid={self.proc.pid} log={self.log_path}")


# ---------------------------------------------------------------- 安全断言


def signed_headers(method, path, body, *, token=SYNC_TOKEN, nonce=None,
                   timestamp=None, signature=None):
    # 时间戳是 RFC3339（服务端按 time.RFC3339 解析），不是 Unix 秒。
    ts = timestamp or datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    nonce = nonce or secrets.token_hex(8)
    mac = hmac.new(token.encode(), f"{ts}\n{nonce}\n".encode(), hashlib.sha256)
    mac.update(body or b"")
    return {
        "Authorization": f"Bearer {token}",
        "X-MindFS-Timestamp": ts,
        "X-MindFS-Nonce": nonce,
        "X-MindFS-Signature": signature if signature is not None else mac.hexdigest(),
        "X-MindFS-Instance": "e2e-verifier",
    }, nonce


def http_call(url, method="GET", headers=None, body=None, cafile=None):
    ctx = None
    if cafile:
        import ssl

        ctx = ssl.create_default_context(cafile=cafile)
    req = urllib.request.Request(url, data=body, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=10, context=ctx) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()


def security_checks(root, binary, primary_url, primary_cafile):
    """鉴权四连 + 清单同档鉴权 + 未启用机器上的 404。"""
    head_url = f"{primary_url}/api/control-sync/head"
    manifest_url = f"{primary_url}/api/control-sync/ui/manifest"

    status, _ = http_call(head_url, cafile=primary_cafile)
    check(status == 401, f"不带签名访问 /head 应 401，得到 {status}")

    headers, _ = signed_headers("GET", "/api/control-sync/head", b"", signature="00" * 32)
    status, _ = http_call(head_url, headers=headers, cafile=primary_cafile)
    check(status == 401, f"签名错应 401，得到 {status}")

    headers, nonce = signed_headers("GET", "/api/control-sync/head", b"")
    status, body = http_call(head_url, headers=headers, cafile=primary_cafile)
    check(status == 200, f"签名正确应 200，得到 {status} {body[:200]!r}")
    info = json.loads(body)
    check(bool(info.get("instance_id")), f"/head 没带主节点身份：{info!r}")

    # 同一 nonce 再来一次：抓到的请求只能成功一次，否则整条通道可重放。
    status, _ = http_call(head_url, headers=headers, cafile=primary_cafile)
    check(status == 401, f"nonce 重放应 401，得到 {status}")

    status, _ = http_call(manifest_url, cafile=primary_cafile)
    check(status == 401, f"无签名取前端清单应 401，得到 {status}")
    headers, _ = signed_headers("GET", "/api/control-sync/ui/manifest", b"")
    status, body = http_call(manifest_url, headers=headers, cafile=primary_cafile)
    check(status == 200, f"签名正确的清单应 200，得到 {status} {body[:200]!r}")
    manifest = json.loads(body)
    check(manifest.get("build_version") == UI_VERSION,
          f"清单版本不是边缘安装的那一版：{manifest.get('build_version')!r}")
    check(len(manifest.get("files") or []) > 10,
          f"清单文件数太少：{len(manifest.get('files') or [])}")

    # 未启用同步的机器：这些路径在路由表里根本不存在 —— token 缺省时的行为。
    plain_xdg = os.path.join(root, "plain")
    os.makedirs(os.path.join(plain_xdg, "mindfs"), exist_ok=True)
    t21.write_config(os.path.join(plain_xdg, "mindfs", "config.json"), {
        "addr": f"127.0.0.1:{PLAIN_PORT}",
        "webPush": False,
    })
    plain_proc = StaticDirInstance("plain", binary, plain_xdg,
                                   os.path.join(root, "logs", "plain.log"), PLAIN_PORT)
    plain = t21.Node("plain", f"http://127.0.0.1:{PLAIN_PORT}")
    plain_proc.start()
    try:
        plain.wait_up()
        for method, path in (("GET", "/api/control-sync/head"),
                             ("POST", "/api/control-sync/push"),
                             ("GET", "/api/control-sync/ui/manifest")):
            status, _ = plain.call(method, path)
            check(status == 404, f"未启用同步的机器上 {method} {path} 应 404，得到 {status}")
    finally:
        plain_proc.stop()
    log("安全断言全部通过")


# ---------------------------------------------------------------- 前端镜像


def wait_mirror(edge_xdg, timeout=120):
    """等边缘把主节点的前端搬下来并切指针。

    指针是 <config>/ui/current（普通文件，Windows 上不需要符号链接权限）。
    """
    ui_dir = os.path.join(edge_xdg, "mindfs", "ui")
    pointer = os.path.join(ui_dir, "current")
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with open(pointer, encoding="utf-8") as fh:
                version = fh.read().strip()
        except OSError:
            time.sleep(1)
            continue
        if version:
            target = os.path.join(ui_dir, version)
            if os.path.exists(os.path.join(target, "index.html")):
                log(f"镜像已切换：{version}")
                return target
        time.sleep(1)
    logs = os.path.join(os.path.dirname(edge_xdg), "logs", "edge.log")
    raise Fail(f"等待前端镜像超时（{timeout}s）：{pointer}")


def mirror_serves_page(edge, root, mirror_dir):
    """把边缘安装目录的 index.html 移走，页面照旧出得来 = 出的是镜像那一版。"""
    install = os.path.join(root, "web-edge")
    hidden = os.path.join(install, "index.html.hidden")
    os.rename(os.path.join(install, "index.html"), hidden)
    try:
        status, body = http_call(edge.base + "/")
        check(status == 200, f"安装副本移走后首页应 200，得到 {status}")
        check(b'<div id="root"' in body,
              "镜像首页里没有应用挂载点：页面不是从镜像出的")
        check(os.path.exists(os.path.join(mirror_dir, "index.html")),
              "镜像目录里没有 index.html")
    finally:
        os.rename(hidden, os.path.join(install, "index.html"))
    log("镜像切换生效（安装副本移走仍出页面）")


# ---------------------------------------------------------------- 浏览器


def wait_text(page, text, timeout, note=""):
    deadline = time.time() + timeout
    last = ""
    while time.time() < deadline:
        try:
            if page.get_by_text(text, exact=False).first.is_visible():
                log(f"看到「{text}」{note}")
                return
        except Exception as exc:  # noqa: BLE001 - 页面刷新期间选择器会抖
            last = str(exc)
        time.sleep(0.5)
    raise Fail(f"等待「{text}」超时（{timeout}s）{note}：{last}")


def wait_banner(page, text, timeout, note=""):
    """横幅专用：只认 role=status 的那块，别被同名的提醒条蒙混过去。"""
    deadline = time.time() + timeout
    last = ""
    while time.time() < deadline:
        try:
            banner = page.locator('div[role="status"]').filter(has_text=text)
            if banner.first.is_visible():
                log(f"横幅出现「{text}」{note}")
                return
        except Exception as exc:  # noqa: BLE001
            last = str(exc)
        time.sleep(0.5)
    raise Fail(f"等待横幅「{text}」超时（{timeout}s）{note}：{last}")


def browser_checks(edge, edge_xdg, primary, primary_proc):
    """真浏览器把四种可见状态走一遍。"""
    status, body = edge.call("POST", "/api/users", BROWSER_USER)
    check(status in (200, 201), f"建浏览器账户 → {status} {body!r}")

    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        try:
            login(page, edge.base, BROWSER_USER)
            wait_banner(page, "Control plane online", 60, "(在线)")

            # 节点不可达：主节点加一台死节点，边缘在线转发会把它返回给页面，
            # 工作台据此明确提示「连不上」，而不是把它显示成「没有项目」。
            status, body = primary.call("POST", "/api/nodes", {
                "id": "dead-node", "name": DEAD_NODE_NAME,
                "url": DEAD_NODE_URL, "remote": True,
            })
            check(status == 200, f"主节点加死节点 → {status} {body!r}")
            wait_node_via_edge(edge, "dead-node", 30)
            page.reload(wait_until="domcontentloaded")
            wait_text(page, "Can't reach node(s)", 90, "(节点不可达)")
            wait_replica_key(edge_xdg, "nodes", DEAD_NODE_URL, 60)

            # 断主节点：边缘降级到本地模式。登录态是前端自己持有的，页面不跳登录。
            primary_proc.stop()
            wait_banner(page, "Primary is unreachable", 90, "(本地模式)")
            # 交接把副本落进权威存储：离线能看到的节点表来自这里，而不是空洞。
            wait_nodes_on_disk(edge_xdg, "dead-node", 30)

            # 两侧都改：离线在边缘改一次，恢复后主节点立刻改成另一个值。
            status, body = edge.call("PUT", t21.PREF_PATH, {"hours": 24})
            check(status == 200, f"离线写偏好 → {status} {body!r}")

            primary_proc.start()
            primary.wait_up()
            status, body = primary.call("PUT", t21.PREF_PATH, {"hours": 36})
            check(status == 200, f"主节点改偏好 → {status} {body!r}")

            wait_banner(page, "Sync conflict", 120, "(冲突模式)")
            page.get_by_role("button", name="View conflicts").first.click()
            wait_text(page, "Sync conflicts", 30, "(冲突面板)")
            wait_text(page, "Preferences", 15, "(冲突面板·域分组)")
            page.get_by_role("button", name="Keep local", exact=True).first.click()

            wait_banner(page, "Control plane online", 120, "(裁决后回到在线)")
            check(primary.prefs_hours() == 24,
                  f"裁决「保留本地」后主节点应是 24，得到 {primary.prefs_hours()}")
        finally:
            browser.close()
    log("浏览器断言全部通过")


def wait_node_via_edge(edge, node_id, timeout):
    """在线时节点的真相在主节点：边缘这份列表是转发回来的，不是本地副本。

    在线刻意不写本地权威存储（D5），所以这里读的是转发结果 —— 用它证明「边缘页面
    看到的节点表就是主节点那份」，别去翻本地 nodes.json（那时还不存在）。
    """
    deadline = time.time() + timeout
    seen = None
    while time.time() < deadline:
        status, body = edge.call("GET", "/api/nodes")
        check(status == 200, f"边缘读节点表 → {status} {body!r}")
        if isinstance(body, list):
            seen = [str(n.get("id")) for n in body if isinstance(n, dict)]
            if node_id in seen:
                return
        time.sleep(0.5)
    raise Fail(f"边缘 {timeout}s 内没从主节点读到节点 {node_id}（最后看到 {seen}）")


def wait_replica_key(xdg, domain, key, timeout):
    """等副本（control_sync.db 的 records）真的拿到这条记录。

    在线时节点表由主节点转发，页面早就看见了；但副本是 5s 一拍往后追的。
    在副本追上之前断网，交接出来的节点表就会少这一条 —— 断网时机必须是证据驱动的。
    """
    path = os.path.join(xdg, "mindfs", "control_sync.db")
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
            try:
                hit = db.execute(
                    "SELECT 1 FROM records WHERE domain = ? AND key = ?",
                    (domain, key)).fetchone()
            finally:
                db.close()
            if hit:
                log(f"副本已追平 {domain}/{key}")
                return
        except sqlite3.Error:
            pass
        time.sleep(1)
    raise Fail(f"副本 {timeout}s 内没拿到 {domain}/{key}：{path}")


def wait_replica_pref_hours(xdg, hours, timeout=30):
    """等副本里的偏好变成刚写下的那个值。

    冲突场景的前提是「两侧从同一个祖先各改各的」：祖先值必须先真的追平到副本，
    否则离线改动的 base_hash 指向的还是一个更老的版本，用例测的就不是我们要验的东西。
    """
    path = os.path.join(xdg, "mindfs", "control_sync.db")
    deadline = time.time() + timeout
    seen = None
    while time.time() < deadline:
        try:
            db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
            try:
                row = db.execute(
                    "SELECT payload FROM records WHERE domain = 'preferences' AND key = ?",
                    (PREF_KEY,)).fetchone()
            finally:
                db.close()
            if row and row[0]:
                # 偏好域的载荷就是字段原值：这个键存的是小时数（裸数字）。
                value = json.loads(row[0])
                seen = value.get("hours") if isinstance(value, dict) else value
                if seen == hours:
                    log(f"副本已追平偏好 hours={hours}")
                    return
        except (sqlite3.Error, ValueError):
            pass
        time.sleep(1)
    raise Fail(f"副本 {timeout}s 内没追平偏好 hours={hours}（最后看到 {seen}）")


def wait_nodes_on_disk(xdg, node_id, timeout):
    """等交接把副本写进权威存储（离线可读的那份）。"""
    path = os.path.join(xdg, "mindfs", "nodes.json")
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with open(path, encoding="utf-8") as fh:
                entries = json.load(fh) or []
            if any(str(e.get("id")) == node_id for e in entries):
                return
        except (OSError, ValueError):
            pass
        time.sleep(1)
    raise Fail(f"边缘副本 {timeout}s 内没拿到节点 {node_id}：{path}")


# ---------------------------------------------------------------- 主流程


def run(root, binary, dist, keep):
    for name in ("web-primary", "web-edge"):
        shutil.copytree(dist, os.path.join(root, name))

    # t21.prepare 负责证书、token 文件与两份 config.json；进程对象这里自己建，
    # 因为每个实例要指定自己那份安装目录（镜像生效与否只能靠「自己那份被移走」证明）。
    primary, edge, _, _, primary_xdg, edge_xdg = t21.prepare(root, binary)
    primary_proc = StaticDirInstance(
        "primary", binary, primary_xdg,
        os.path.join(root, "logs", "primary.log"), t21.PRIMARY_PORT,
        static_dir=os.path.join(root, "web-primary"))
    edge_proc = StaticDirInstance(
        "edge", binary, edge_xdg,
        os.path.join(root, "logs", "edge.log"), t21.EDGE_PORT,
        static_dir=os.path.join(root, "web-edge"))

    failed = False
    try:
        primary_proc.start()
        primary.wait_up()
        edge_proc.start()
        edge.wait_up()
        t21.wait_mode(edge, "online", 45, "(初始)")

        security_checks(root, binary, primary.base, primary.cafile)

        mirror_dir = wait_mirror(edge_xdg, 180)
        mirror_serves_page(edge, root, mirror_dir)

        # 在线一次写：给冲突准备一个共同的祖先值（并等它真的追平到副本）。
        status, body = edge.call("PUT", t21.PREF_PATH, {"hours": 12})
        check(status == 200, f"在线写偏好 → {status} {body!r}")
        wait_replica_pref_hours(edge_xdg, 12)

        browser_checks(edge, edge_xdg, primary, primary_proc)
        log("全部 PASS")
    except Fail as exc:
        failed = True
        print(f"\n[e2e] FAIL: {exc}", file=sys.stderr)
        for inst in (primary_proc, edge_proc):
            print(f"\n--- {inst.name} 日志尾部 ---", file=sys.stderr)
            print(inst.tail(80), file=sys.stderr)
        raise
    finally:
        edge_proc.stop()
        primary_proc.stop()
        if not keep and not failed:
            shutil.rmtree(root, ignore_errors=True)
        else:
            log(f"保留现场：{root}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="", help="mindfs 可执行文件；缺省现编一个")
    parser.add_argument("--dist", default="", help="前端产物目录；缺省现编一份 base=/ 的")
    parser.add_argument("--root", default="/tmp/mindfs-control-sync-t22",
                        help="两个实例的 XDG_CONFIG_HOME、证书与前端产物都建在这里")
    parser.add_argument("--keep", action="store_true", help="结束后保留现场")
    args = parser.parse_args()

    root = os.path.abspath(args.root)
    if os.path.exists(root):
        shutil.rmtree(root)
    os.makedirs(root, exist_ok=True)
    os.makedirs(os.path.join(root, "logs"), exist_ok=True)

    binary = os.path.abspath(args.binary) if args.binary else build_binary(root)
    dist = os.path.abspath(args.dist) if args.dist else build_dist(root)

    try:
        run(root, binary, dist, args.keep)
    except Fail:
        sys.exit(1)


if __name__ == "__main__":
    main()
