#!/usr/bin/env bash
# iOS 壳出包（iPad / iPhone）。**只能在 macOS + Xcode 上跑得通**：xcodebuild 没有别的实现。
#
# 装机三条路（免费、自用）见 docs/ios-shell.md。这个脚本负责其中两条：
#   1) 借一台 Mac：同步资源 → 编译 → 归档 → 导出自签 .ipa（个人免费 Apple ID 即可，签名 7 天有效）
#   2) 把 .ipa 拿到 Windows 上用 Sideloadly 装进 iPad
# 完全没有 Mac 时不要跑这个脚本：那条路是 Safari「添加到主屏」，不产出 .ipa。
#
# 用法：
#   DEVELOPMENT_TEAM=ABCDE12345 bash scripts/build-ipa.sh      # 完整链，产出 dist/mindfs_<版本>_ios.ipa
#   bash scripts/build-ipa.sh --build-only                     # 只编译不出 ipa（第一次排错用）
#   bash scripts/build-ipa.sh --open                           # 只同步资源并打开 Xcode（手点 Run 直接装到连着的 iPad）
#   bash scripts/build-ipa.sh --skip-sync                      # 跳过 make build-ios（资源已经是最新时）
#
# Team ID：Xcode → Settings → Accounts → 你的 Apple ID → Personal Team 那行的 10 位编号。
# 个人免费账号的签名只有 7 天：过期后 App 打不开，重跑这个脚本（或用 Xcode 重连 iPad 再 Run 一次）即续。
#
# ⚠️ 本仓库的开发机没有 macOS，这个脚本未在 Mac 上实跑过（AGENTS.md 的「必须 Mac 才能验的部分」）。
#    第一次跑请把 xcodebuild 的报错原样贴回来，不要自己改工程文件猜。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IOS_DIR="$ROOT/web/ios/App"
PROJECT="$IOS_DIR/App.xcodeproj"
SCHEME="App"
DIST_DIR="$ROOT/dist"

say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }

BUILD_ONLY=0
OPEN_XCODE=0
SKIP_SYNC=0
for arg in "$@"; do
  case "$arg" in
    --build-only) BUILD_ONLY=1 ;;
    --open) OPEN_XCODE=1 ;;
    --skip-sync) SKIP_SYNC=1 ;;
    -h|--help) sed -n '2,26p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) die "未知参数 $arg（见 --help）" ;;
  esac
done

[ "$(uname -s)" = "Darwin" ] || die "xcodebuild 只在 macOS 上存在。这台机器上只能做资源同步：make build-ios"
command -v xcodebuild >/dev/null 2>&1 || die "找不到 xcodebuild，请安装 Xcode 并跑 xcode-select --install"
[ -d "$PROJECT" ] || die "缺少 iOS 工程 $PROJECT"

# ── 版本：MARKETING_VERSION 只吃 semver 核心三段，构建号进 CURRENT_PROJECT_VERSION ──
# 与 parseVersionParts（Go）/ appVersion.ts 的 versionParts 同一条读法：先剥掉 git describe 的
# 装饰（-<commits>-g<sha> 与 -dirty），再把 '.' 与 '-' 都当分隔符，取前导数字段，撞见非数字段即停。
# iOS 侧不需要安卓那条 versionCode 公式（没有安装器排序问题），CFBundleVersion 直接写构建号。
step "版本"
VERSION_RAW="$(cd "$ROOT" && git describe --tags --always --dirty 2>/dev/null || echo dev)"
CORE="${VERSION_RAW#v}"
CORE="${CORE%-dirty}"
CORE="$(printf '%s' "$CORE" | sed -E 's/-[0-9]+-g[0-9a-fA-F]+$//')"
read -r V_MAJOR V_MINOR V_PATCH V_BUILD <<< "$(printf '%s' "$CORE" | tr '.-' '  ' | awk '{ out=""; for (i = 1; i <= NF; i++) { if ($i !~ /^[0-9]+$/) break; out = out " " $i } print out }')" || true
[ -n "$V_MAJOR" ] && [ -n "$V_MINOR" ] && [ -n "$V_PATCH" ] || \
  die "从「$VERSION_RAW」读不出三段 semver。发布构建请带 tag（git tag v0.5.5-1115），或自己起一个本地 tag"
BUILD_NUMBER="${V_BUILD:-1}"
MARKETING_VERSION="$V_MAJOR.$V_MINOR.$V_PATCH"
CURRENT_PROJECT_VERSION="$BUILD_NUMBER"
say "  git describe     : $VERSION_RAW"
say "  MARKETING_VERSION: $MARKETING_VERSION"
say "  CURRENT_PROJECT_VERSION: $CURRENT_PROJECT_VERSION"

if [ "$SKIP_SYNC" != 1 ]; then
  step "同步前端产物（make build-ios：build:shell → cap sync ios）"
  (cd "$ROOT" && make build-ios VERSION="$VERSION_RAW")
fi

if [ "$OPEN_XCODE" = 1 ]; then
  step "打开 Xcode"
  say "  连上 iPad（数据线）→ 选中你的设备 → Run。第一次要在设备里信任开发者证书。"
  exec open "$PROJECT"
fi

if [ "$BUILD_ONLY" = 1 ]; then
  step "只编译（不出 ipa）"
  (cd "$IOS_DIR" && xcodebuild -project "$PROJECT" -scheme "$SCHEME" -configuration Release \
    -destination 'generic/platform=iOS' -derivedDataPath "$IOS_DIR/build/derived" \
    MARKETING_VERSION="$MARKETING_VERSION" CURRENT_PROJECT_VERSION="$CURRENT_PROJECT_VERSION" \
    build)
  say "编译通过。出 ipa 需要 DEVELOPMENT_TEAM=<10 位 Team ID> 重跑（不带 --build-only）。"
  exit 0
fi

[ -n "${DEVELOPMENT_TEAM:-}" ] || die "缺 DEVELOPMENT_TEAM。Xcode → Settings → Accounts → Personal Team 的 10 位编号，然后 DEVELOPMENT_TEAM=XXXX bash scripts/build-ipa.sh"
case "$DEVELOPMENT_TEAM" in
  *[!A-Za-z0-9]*|'') die "DEVELOPMENT_TEAM 只允许字母数字，收到「$DEVELOPMENT_TEAM」" ;;
esac

mkdir -p "$DIST_DIR"
ARCHIVE="$DIST_DIR/mindfs_${VERSION_RAW}_ios.xcarchive"
EXPORT_OPTIONS="$DIST_DIR/ios-export-options.plist"

cat > "$EXPORT_OPTIONS" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>method</key>
	<string>development</string>
	<key>destination</key>
	<string>export</string>
	<key>signingStyle</key>
	<string>automatic</string>
	<key>teamID</key>
	<string>${DEVELOPMENT_TEAM}</string>
	<key>compileBitcode</key>
	<false/>
	<key>stripSwiftSymbols</key>
	<true/>
	<key>uploadSymbols</key>
	<false/>
</dict>
</plist>
PLIST

step "归档（archive）"
(cd "$IOS_DIR" && xcodebuild -project "$PROJECT" -scheme "$SCHEME" -configuration Release \
  -destination 'generic/platform=iOS' \
  -derivedDataPath "$IOS_DIR/build/derived" \
  -archivePath "$ARCHIVE" \
  MARKETING_VERSION="$MARKETING_VERSION" CURRENT_PROJECT_VERSION="$CURRENT_PROJECT_VERSION" \
  CODE_SIGN_STYLE=Automatic DEVELOPMENT_TEAM="$DEVELOPMENT_TEAM" \
  archive)

step "导出 ipa"
(cd "$IOS_DIR" && xcodebuild -exportArchive -archivePath "$ARCHIVE" \
  -exportOptionsPlist "$EXPORT_OPTIONS" -exportPath "$DIST_DIR")

EXPORTED="$(find "$DIST_DIR" -maxdepth 1 -name '*.ipa' | head -1)"
[ -n "$EXPORTED" ] || die "xcodebuild 自称导出成功但 dist/ 里没有 .ipa"
TARGET="$DIST_DIR/mindfs_${VERSION_RAW}_ios.ipa"
if [ "$EXPORTED" != "$TARGET" ]; then
  mv "$EXPORTED" "$TARGET"
fi
rm -f "$EXPORT_OPTIONS"

step "产物"
say "  $TARGET"
shasum -a 256 "$TARGET" 2>/dev/null || sha256sum "$TARGET" 2>/dev/null || true
say ""
say "装机："
say "  · 这台 Mac 上：Xcode → Window → Devices and Simulators → 拖 ipa 到已连的 iPad，或用 Xcode 直接 Run 源码"
say "  · Windows 上：Sideloadly 选这个 .ipa + 同一个 Apple ID → 装进 iPad（iPad 里 设置 → 通用 → VPN 与设备管理 信任开发者）"
say "  · 签名 7 天有效，过期后重跑本脚本续期。详见 docs/ios-shell.md"
