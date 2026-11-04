#!/usr/bin/env bash
set -u

fail=0
say() { printf '%s\n' "$*"; }
ok() { say "  [OK] $*"; }
bad() { say "  [FAIL] $*"; fail=1; }

JAVA_HOME="${JAVA_HOME:-}"
if [ -z "$JAVA_HOME" ] && command -v java >/dev/null 2>&1; then
  JAVA_HOME="$(java -XshowSettings:properties -version 2>&1 | sed -n 's/.*java.home = //p' | head -1)"
fi

say "== MindFS Android doctor =="

# Java
if [ -n "$JAVA_HOME" ] && [ -x "$JAVA_HOME/bin/java" ]; then
  jv=$("$JAVA_HOME/bin/java" -version 2>&1 | head -1)
  if echo "$jv" | grep -q '"21\.' ; then
    ok "Java 21: $JAVA_HOME ($jv)"
  else
    bad "Java 不是 21: $JAVA_HOME ($jv)。请安装 openjdk-21-jdk-headless"
  fi
else
  bad "JAVA_HOME 未设置且 java 不在 PATH。sudo apt-get install -y openjdk-21-jdk-headless 后 export JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64"
fi

# SDK
SDK_ROOT="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
if [ -z "$SDK_ROOT" ]; then
  bad "ANDROID_SDK_ROOT / ANDROID_HOME 均未设置。export ANDROID_SDK_ROOT=\$HOME/Android/Sdk 或在 web/android/local.properties 写 sdk.dir=..."
else
  if [ -d "$SDK_ROOT/platforms/android-36" ]; then ok "platforms;android-36"; else bad "缺少 platforms;android-36（sdkmanager \"platforms;android-36\"）"; fi
  if [ -x "$SDK_ROOT/build-tools/36.0.0/apksigner" ]; then ok "build-tools;36.0.0 (apksigner)"; else bad "缺少 build-tools;36.0.0"; fi
  if [ -x "$SDK_ROOT/platform-tools/adb" ]; then ok "platform-tools (adb)"; else bad "缺少 platform-tools"; fi
fi

# Capacitor CLI
if [ -x "web/node_modules/.bin/cap" ]; then ok "Capacitor CLI"; else bad "缺少 web/node_modules/.bin/cap：cd web && npm ci"; fi
if [ -f "web/package-lock.json" ]; then ok "package-lock.json"; else bad "缺少 web/package-lock.json"; fi

# keystore
if [ -f "web/android/keystore.properties" ]; then ok "web/android/keystore.properties"; else
  if [ -n "${ANDROID_KEYSTORE_FILE:-}" ] && [ -n "${ANDROID_KEYSTORE_PASSWORD:-}" ] && [ -n "${ANDROID_KEY_ALIAS:-}" ] && [ -n "${ANDROID_KEY_PASSWORD:-}" ]; then
    ok "ANDROID_KEYSTORE_* 环境变量"
  else
    bad "缺少签名配置：创建 web/android/keystore.properties 或设置 ANDROID_KEYSTORE_*（见 docs/android-build.md）"
  fi
fi

# gradlew
if [ -x "web/android/gradlew" ]; then ok "gradlew"; else bad "android/gradlew 不可执行"; fi

if [ "$fail" -ne 0 ]; then
  say ""
  say "预检未通过，请按上面的提示处理后重跑 make android-doctor。"
  exit 1
fi
say ""
say "预检通过。"
exit 0
