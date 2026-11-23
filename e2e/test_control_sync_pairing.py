#!/usr/bin/env python3
"""W2 双实例隔离验收：边控端握手（配对）+ 自动同步 + 踢人。

两个真实进程、两份独立 XDG_CONFIG_HOME，端口 7470/7471，绝不碰 7331：

  主节点 A  https://127.0.0.1:7470  control_sync.role=primary（token 自生成）
  边缘   B  http://127.0.0.1:7471   control_sync.role=edge，token_file 指向不存在的文件

场景：

  0. 边缘以「没有 token」起：服务起得来，只挂配对端点（GET /pair 200、/status 404）；
  1. POST /api/control-sync/pair 填主控端地址 + token + CA → 200 + restart_required；
     落盘：token_file 写入、config.json 的 control_sync 段改为 edge；
  2. 重启边缘 → 配对端点消失、同步端点出现，模式追到 online；
  3. 主节点 /edges 名单里出现这台边控端；
  4. 主节点签名踢人 → 边缘降级并停在本地模式，last_seen_at 不再推进。

复用 test_control_sync_offline.py 的进程/证书/HTTP 装置（同一套 ovserver 口径），
只补配对相关的断言。为什么不随便调 t21.prepare()：那里的边缘一上来就带着 token，
正是本脚本要验的反面。
"""

from __future__ import annotations

import argparse
import datetime
import hashlib
import hmac
import json
import os
import secrets
import shutil
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request

E2E_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, E2E_DIR)
import test_control_sync_offline as t21  # noqa: E402 - 复用同一套装置

Fail = t21.Fail
check = t21.check
log = t21.log
Node = t21.Node
Instance = t21.Instance
make_certs = t21.make_certs
write_config = t21.write_config
mode_of = t21.mode_of
wait_mode = t21.wait_mode
wait_mode_stable = t21.wait_mode_stable

PRIMARY_PORT = 7470
EDGE_PORT = 7471
INSTANCE_ID = "e2e-edge"


# ---------------------------------------------------------------- signed calls


def signed_call(node, method, path, *, token, body=None, instance_id="e2e-verifier"):
    """主节点同步端点一律走 HMAC 签名（与 auth.go 的 SyncSignature 同口径）。"""
    ts = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    nonce = secrets.token_hex(8)
    raw = b"" if body is None else json.dumps(body).encode()
    mac = hmac.new(token.encode(), f"{ts}\n{nonce}\n".encode(), hashlib.sha256)
    mac.update(raw)
    headers = {
        "Authorization": f"Bearer {token}",
        "X-MindFS-Timestamp": ts,
        "X-MindFS-Nonce": nonce,
        "X-MindFS-Signature": mac.hexdigest(),
        "X-MindFS-Instance": instance_id,
    }
    if body is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(node.base + path, data=raw or None,
                                 method=method, headers=headers)
    ctx = ssl.create_default_context(cafile=node.cafile) if node.cafile else None
    try:
        with urllib.request.urlopen(req, timeout=10, context=ctx) as resp:
            raw_resp = resp.read()
            status = resp.status
    except urllib.error.HTTPError as exc:
        raw_resp = exc.read()
        status = exc.code
    try:
        parsed = json.loads(raw_resp) if raw_resp else None
    except json.JSONDecodeError:
        parsed = raw_resp.decode(errors="replace")
    return status, parsed


def read_token(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read().strip()
    except FileNotFoundError:
        return ""


def read_config(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def edge_in_list(primary, token, instance_id=INSTANCE_ID):
    status, body = signed_call(primary, "GET", "/api/control-sync/edges", token=token)
    check(status == 200, f"主节点 /edges → {status} {body!r}")
    rows = body if isinstance(body, list) else body.get("edges", [])
    for row in rows:
        if row.get("instance_id") == instance_id:
            return row
    return None


# ---------------------------------------------------------------- fixtures


def prepare(root, binary):
    for sub in ("primary", "edge"):
        os.makedirs(os.path.join(root, sub, "mindfs"), exist_ok=True)
    os.makedirs(os.path.join(root, "logs"), exist_ok=True)
    ca_crt, srv_crt, srv_key = make_certs(root)

    primary_xdg = os.path.join(root, "primary")
    edge_xdg = os.path.join(root, "edge")
    primary_cfg = os.path.join(primary_xdg, "mindfs", "config.json")
    edge_cfg = os.path.join(edge_xdg, "mindfs", "config.json")

    # 主节点的 token 由装配层自己生成（首次上线不该要求用户先手搓密钥），
    # 所以这里只给路径，文件在进程起来之后才出现。
    primary_token = os.path.join(root, "primary.token")
    # 边缘的 token 文件一开始**不存在** —— 正是「这台机器还没配对」的判据。
    edge_token = os.path.join(root, "edge.token")

    write_config(primary_cfg, {
        "addr": f"127.0.0.1:{PRIMARY_PORT}",
        "tls": True,
        "cert": srv_crt,
        "key": srv_key,
        "webPush": False,
        "control_sync": {
            "enabled": True,
            "role": "primary",
            "instance_id": "e2e-primary",
            "token_file": primary_token,
        },
    })
    write_config(edge_cfg, {
        "addr": f"127.0.0.1:{EDGE_PORT}",
        "webPush": False,
        "control_sync": {
            "enabled": True,
            "role": "edge",
            "instance_id": INSTANCE_ID,
            "primary_url": f"https://127.0.0.1:{PRIMARY_PORT}",
            "ca_file": ca_crt,
            "token_file": edge_token,
        },
    })

    primary = Node("primary", f"https://127.0.0.1:{PRIMARY_PORT}", cafile=ca_crt)
    edge = Node("edge", f"http://127.0.0.1:{EDGE_PORT}")
    primary_proc = Instance("primary", binary, primary_xdg,
                            os.path.join(root, "logs", "primary.log"), PRIMARY_PORT)
    edge_proc = Instance("edge", binary, edge_xdg,
                         os.path.join(root, "logs", "edge.log"), EDGE_PORT)
    return {
        "primary": primary, "edge": edge,
        "primary_proc": primary_proc, "edge_proc": edge_proc,
        "primary_cfg": primary_cfg, "edge_cfg": edge_cfg,
        "primary_token": primary_token, "edge_token": edge_token,
        "ca": ca_crt,
    }


def run(root, binary, keep):
    env = prepare(root, binary)
    primary, edge = env["primary"], env["edge"]
    primary_proc, edge_proc = env["primary_proc"], env["edge_proc"]

    failed = False
    try:
        # ---------------- 0. 未配对边缘：起得来，但只有配对端点
        primary_proc.start()
        primary.wait_up()
        token = read_token(env["primary_token"])
        check(len(token) >= 16, f"主节点没生成同步 token（读到的长度 {len(token)}）")
        log(f"主节点自生成 token 长度 {len(token)}")

        check(not os.path.exists(env["edge_token"]), "前提：边缘此时不该有 token 文件")
        edge_proc.start()
        edge.wait_up()

        status, body = edge.call("GET", "/api/control-sync/pair")
        check(status == 200, f"未配对边缘的配对探针 → {status} {body!r}")
        check(body.get("pending_pairing") is True,
              f"未配对边缘必须自报 pending：{body!r}")
        check(body.get("instance_id") == INSTANCE_ID,
              f"探针应回显实例名：{body!r}")
        # 探针不许带出 token / token 路径 / 同步状态。
        leaked = token in json.dumps(body) or env["edge_token"] in json.dumps(body)
        check(not leaked, f"配对探针泄露了内部信息：{body!r}")

        status, body = edge.call("GET", "/api/control-sync/status")
        check(status == 404, f"未配对时不该有同步状态端点 → {status} {body!r}")

        # ---------------- 1. 配对：填地址 + token + CA
        status, body = edge.call("POST", "/api/control-sync/pair", {
            "primary_url": f"https://127.0.0.1:{PRIMARY_PORT}",
            "token": token,
            "ca_file": env["ca"],
            "instance_id": INSTANCE_ID,
        }, timeout=30)
        check(status == 200, f"配对 → {status} {body!r}")
        check(body.get("status") == "paired", f"配对响应 → {body!r}")
        check(body.get("restart_required") is True,
              f"配对只落盘、必须要求重启：{body!r}")
        check(body.get("instance_id") == INSTANCE_ID, f"配对响应实例名 → {body!r}")
        check(token not in json.dumps(body), f"配对响应不该回显 token：{body!r}")

        check(read_token(env["edge_token"]) == token, "token_file 没落盘成提交的那个 token")
        mode = os.stat(env["edge_token"]).st_mode & 0o777
        check(mode == 0o600, f"token_file 权限应为 0600，实际 {oct(mode)}")

        cfg = read_config(env["edge_cfg"])
        section = cfg.get("control_sync") or {}
        check(section.get("enabled") is True, f"control_sync.enabled 没写回：{cfg!r}")
        check(section.get("role") == "edge", f"control_sync.role 不对：{cfg!r}")
        check(section.get("primary_url") == f"https://127.0.0.1:{PRIMARY_PORT}",
              f"primary_url 没写回：{cfg!r}")
        check(section.get("token_file") == env["edge_token"], f"token_file 不对：{cfg!r}")
        check(section.get("ca_file") == env["ca"], f"ca_file 不对：{cfg!r}")
        # 边缘必须出页面（主控端不可达时要靠它继续用），worker 角色会被纠正。
        check(cfg.get("role", "control") != "worker",
              f"配对后仍是 worker 角色，页面会 403：{cfg!r}")
        # 配对只管 control_sync 这一段：别把用户手写的键卷走。
        check(cfg.get("addr") == f"127.0.0.1:{EDGE_PORT}", f"addr 被改了：{cfg!r}")

        # 落盘 ≠ 生效：当前进程仍是启动期装配的「等配对」档。
        status, _ = edge.call("GET", "/api/control-sync/status")
        check(status == 404, f"没重启就不该有同步端点（实际 {status}）")

        # ---------------- 2. 重启后接上
        edge_proc.stop()
        edge_proc.start()
        edge.wait_up()

        # 配对端点不会消失：已配好的边缘照样留着它，好就地换主控端
        # （见 server/app/control_sync.go 的 else 分支）。但它必须不再自报 pending ——
        # 前端只把 pending_pairing=true 当「等配对」，其余一律折成 null。
        status, body = edge.call("GET", "/api/control-sync/pair")
        check(status == 200, f"已配对边缘的配对探针应仍可读 → {status} {body!r}")
        check(body.get("pending_pairing") is False,
              f"已配对边缘不得自报 pending（前端会一直显示配对表单）→ {body!r}")
        check(body.get("instance_id") == INSTANCE_ID, f"探针实例名 → {body!r}")
        # 同步端点反过来出现（未配对时它们是 404）。装配错了的话这里第一个红。
        status, body = edge.call("GET", "/api/control-sync/status")
        check(status == 200, f"配对并重启后应有同步状态端点 → {status} {body!r}")
        wait_mode_stable(edge, "online", 60, 5, "(配对后)")

        # ---------------- 3. 主节点认得这台边控端
        row = edge_in_list(primary, token)
        check(row is not None, "主节点 /edges 名单里没有这台边控端")
        check(not row.get("revoked_at"), f"刚接上就被标记踢出：{row!r}")
        check(bool(row.get("last_seen_at")), f"last_seen_at 为空：{row!r}")

        # ---------------- 4. 踢人：降级并停在本地
        status, body = signed_call(primary, "POST",
                                   f"/api/control-sync/edges/{INSTANCE_ID}/revoke", token=token)
        check(status == 200, f"踢人 → {status} {body!r}")
        check(body.get("revoked_at"), f"踢人响应没带 revoked_at：{body!r}")
        # 基线必须在踢人**之后**取：踢之前发出去的那一拍可能刚好在这一刻落库，
        # 拿更早的读数当基线会误报「被踢后还在推进」。
        frozen = edge_in_list(primary, token)
        check(frozen is not None, "踢人后名单里丢了这台边控端（要保留封禁记录）")
        seen_before = frozen.get("last_seen_at")

        # 3 次探活失败才降级（10s 一拍），所以这里给足时间。
        wait_mode_stable(edge, "offline-local", 90, 3, "(被踢后)")
        status, body = edge.call("GET", "/api/control-sync/status", timeout=5)
        check(status == 200, f"被踢后状态端点仍应可读：{status} {body!r}")
        check(body.get("mode") == "offline-local", f"被踢后模式 → {body!r}")

        # 被踢的边控端不该再刷新 last_seen_at —— 否则名单上「最近在线」会永远说谎。
        # 探活 10s 一拍，等两拍才有说服力（一次 200 可能只是刚好在踢之前发出去的）。
        time.sleep(22)
        row_after = edge_in_list(primary, token)
        check(row_after is not None, "被踢后名单里丢了这台边控端（要保留封禁记录）")
        check(row_after.get("revoked_at"), f"revoked_at 被覆盖掉了：{row_after!r}")
        check(row_after.get("last_seen_at") == seen_before,
              f"被踢后 last_seen_at 仍在推进：{seen_before} → {row_after.get('last_seen_at')}")

        log("W2 配对 + 自动同步 + 踢人 全部通过")
    except Fail:
        failed = True
        raise
    finally:
        for proc in (edge_proc, primary_proc):
            try:
                proc.stop()
            except Exception:  # noqa: BLE001 - 收尾阶段不该掩盖原始失败
                pass
        if failed:
            log(f"现场保留在 {root}")
            for proc in (env["primary_proc"], env["edge_proc"]):
                log(f"---- {proc.name}.log 尾部 ----\n{proc.tail(40)}")
        elif not keep:
            shutil.rmtree(root, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="", help="现成的 mindfs 二进制（缺省现编）")
    parser.add_argument("--root", default="/tmp/mindfs-control-sync-pairing",
                        help="隔离根目录（启动先整个删）")
    parser.add_argument("--keep", action="store_true", help="成功后也保留现场")
    args = parser.parse_args()

    root = os.path.abspath(args.root)
    if os.path.exists(root):
        shutil.rmtree(root)
    os.makedirs(root, exist_ok=True)

    binary = args.binary
    if not binary:
        binary = os.path.join(root, "mindfs")
        log("编译 bin（go build ./cli/cmd）")
        subprocess.run(["go", "build", "-o", binary, "./cli/cmd"], check=True)
    binary = os.path.abspath(binary)

    try:
        run(root, binary, args.keep)
    except Fail:
        sys.exit(1)


if __name__ == "__main__":
    main()
