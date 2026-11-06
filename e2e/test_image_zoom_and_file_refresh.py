"""图片缩放与文件自动刷新（G-KK / G-KL）的隔离实例验收。

只断言用户看得见的东西：
  - 文件视图里的图片能放大、能进全屏灯箱、Escape 收场；
  - 同名图片在磁盘上被改写后，重新打开看到的是新图（版本化缓存没漏层）；
  - 打开状态下改写图片，不点任何按钮也会自己换成新图（轮询/事件通道）。

图用纯 stdlib 现造（两张尺寸不同，naturalWidth 就是判据），不引第三方依赖。
"""

from __future__ import annotations

import struct
import time
import zlib

from playwright.sync_api import Page, expect

PROJECT = "mindfs-e2e-project0"
IMAGE = "zoom-shot.png"


def _png(width: int, height: int, rgb: tuple[int, int, int]) -> bytes:
    """最小 RGB PNG（每行 filter=0），够浏览器解码即可。"""
    raw = b"".join(b"\x00" + bytes(rgb) * width for _ in range(height))

    def chunk(tag: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


def _show_files(page: Page) -> None:
    switcher = '[data-onboarding="main-view-switcher"]'
    page.locator(switcher).first.get_by_role("tab", name="Files", exact=True).click()
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=20_000)


def _open_tree_file(page: Page, name: str) -> None:
    """刷新项目树后点开文件（新写的文件要靠刷新才出现在树里）。"""
    page.locator('[data-onboarding="sidebar-refresh"]').first.click()
    page.get_by_text(name, exact=True).first.wait_for(timeout=20_000)
    page.get_by_text(name, exact=True).first.click()


def _image_width(page: Page) -> int:
    return int(page.eval_on_selector(f'img[alt="{IMAGE}"]', "el => el.naturalWidth"))


def test_image_zoom_inline_and_lightbox(authed_page: Page, project_dir) -> None:
    page = authed_page
    (project_dir / IMAGE).write_bytes(_png(64, 64, (200, 30, 30)))
    _show_files(page)
    _open_tree_file(page, IMAGE)

    image = page.locator(f'img[alt="{IMAGE}"]').first
    expect(image).to_be_visible(timeout=20_000)
    assert _image_width(page) == 64

    # 内联缩放：工具栏在，点 + 到 150%。
    # 百分比按钮带 aria-label，可访问名不是显示文本，所以按文本定位。
    page.get_by_role("button", name="Zoom in").first.click()
    expect(page.get_by_text("150%", exact=True).first).to_be_visible(timeout=10_000)
    page.get_by_role("button", name="Reset to fit screen").first.click()
    expect(page.get_by_text("100%", exact=True).first).to_be_visible(timeout=10_000)

    # 全屏灯箱：打开 → 放大 → Escape 收场。
    page.get_by_role("button", name="View image full screen").first.click()
    dialog = page.locator("dialog.image-zoom")
    expect(dialog).to_be_visible(timeout=10_000)
    dialog.get_by_role("button", name="Zoom in").click()
    expect(dialog.get_by_text("150%", exact=True)).to_be_visible(timeout=10_000)
    page.keyboard.press("Escape")
    expect(dialog).to_have_count(0, timeout=10_000)


def test_reopened_image_shows_rewritten_file(authed_page: Page, project_dir) -> None:
    """原 bug 的复现路径：改写同名图片 → 切走再切回来，必须是新图。"""
    page = authed_page
    (project_dir / IMAGE).write_bytes(_png(64, 64, (200, 30, 30)))
    _show_files(page)
    _open_tree_file(page, IMAGE)
    expect(page.locator(f'img[alt="{IMAGE}"]').first).to_be_visible(timeout=20_000)
    assert _image_width(page) == 64

    # 切到别的文件让预览卸载，再在磁盘上改写同名图片。
    page.get_by_text("README.md", exact=True).first.click()
    page.wait_for_selector("text=hello from e2e", timeout=20_000)
    (project_dir / IMAGE).write_bytes(_png(96, 96, (30, 30, 200)))
    time.sleep(1.5)  # 让 fsnotify 的去抖窗口过去，避免只测到「事件刚好赶上了」

    _open_tree_file(page, IMAGE)
    expect(page.locator(f'img[alt="{IMAGE}"]').first).to_be_visible(timeout=20_000)
    assert _image_width(page) == 96, "重新打开还是旧图 —— 版本化缓存的某一层漏了"


def test_open_image_refreshes_without_user_action(authed_page: Page, project_dir) -> None:
    """打开状态下改写图片：不点任何按钮，界面自己换成新图（轮询/事件通道）。"""
    page = authed_page
    (project_dir / IMAGE).write_bytes(_png(64, 64, (200, 30, 30)))
    _show_files(page)
    _open_tree_file(page, IMAGE)
    expect(page.locator(f'img[alt="{IMAGE}"]').first).to_be_visible(timeout=20_000)
    assert _image_width(page) == 64

    (project_dir / IMAGE).write_bytes(_png(128, 128, (30, 200, 30)))
    page.wait_for_function(
        f'document.querySelector(\'img[alt="{IMAGE}"]\')?.naturalWidth === 128',
        timeout=25_000,
    )

    # 手动刷新按钮也在（自动通道之外的兜底入口）。
    expect(page.get_by_role("button", name="Refresh file").first).to_be_visible(timeout=10_000)
