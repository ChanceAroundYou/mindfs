.PHONY: help dev dev-backend dev-web build-web build build-android build-ios install uninstall build-all start start-server test test-web check-upstream mutate dist-clean publish-release-notes verify-release release tag

GO ?= go
NPM ?= npm
WEB_DIR ?= web
ANDROID_DIR ?= web/android
IOS_DIR ?= web/ios
ADDR ?= :7331
ROOT ?= .
PREFIX ?= $(HOME)/.local
# 旧构建残留的保留期（天）。哈希资源对外是 immutable，删太快会让部署前就打开的
# 标签页在懒加载时 404（前端 staleAssetRecovery 只能兜底一次重载）。
# 每次构建都会换掉全部 chunk 的哈希（buildStamp 随机），所以保留期直接乘成磁盘占用：
# 14 天曾堆到 14,632 个文件 / 705 MB；打开着的标签页只需跨一次部署，一天够用。
WEB_ASSET_TTL_DAYS ?= 1

help:
	@printf "%s\n" \
		"Targets:" \
		"  make dev          # run mindfs on $(ADDR)" \
		"  make dev-backend  # backend only on $(ADDR)" \
		"  make dev-web      # Vite dev server only" \
		"  make build-web    # build web assets into web/dist" \
		"  make build        # build web assets and CLI binary" \
		"  make build-android # build Android release APK into dist/" \
		"  make build-ios    # sync the shared shell bundle into the iOS Xcode project (packaging needs a Mac)" \
		"  make install      # install binary and built static assets into $(PREFIX)" \
		"  make uninstall    # remove installed binary and static assets from $(PREFIX)" \
		"  make build-all    # cross-compile for all platforms into dist/" \
		"  make dist-clean   # remove dist/ directory" \
		"  make start        # run mindfs on $(ADDR) with built static assets" \
		"  make start-server # backend entrypoint serving built static assets" \
		"  make test         # run Go tests" \
		"  make test-web     # run web source-guard tests" \
		"  make check-upstream  # fork 定制覆盖率门禁（需 upstream ref）" \
		"  make mutate       # 变异账本：注入已知错误改法，确认有测试守得住" \
		"  make tag TAG=v1.2.3  # create and push a git tag" \
		"  make publish-release-notes TAG=v1.2.3  # commit and push release-notes.md if changed" \
		"  make verify-release TAG=v1.2.3  # verify signed release manifest and artifacts in $(DIST_DIR)" \
		"  make release TAG=v1.2.3  # publish notes, build-all, then create GitHub release" \
		"  make release TAG=v1.2.3 RELEASE_ANDROID=1  # include Android APK"

# 部署前缀：前端 VITE_MIND_FS_BASE 与后端 DeployPrefix 必须一致。
# 直接包含 web/.env，优先取环境/命令行的 MIND_FS_BASE；未配置时回退 /mindfs。
# 根部署通过显式 MIND_FS_BASE=/ make build 选择，避免将空环境变量和未配置混淆。
-include $(WEB_DIR)/.env
MIND_FS_BASE ?= $(VITE_MIND_FS_BASE)
ifeq ($(strip $(MIND_FS_BASE)),)
MIND_FS_BASE := /mindfs
endif

dev:
	$(GO) run ./cli/cmd -addr $(ADDR) $(ROOT)

dev-backend:
	$(GO) run ./server/cmd/mindfs-server -addr $(ADDR)

dev-web:
	cd $(WEB_DIR) && $(NPM) run dev

build-web:
	cd $(WEB_DIR) && VITE_MIND_FS_BASE="$(MIND_FS_BASE)" $(NPM) run build
	$(GO) run ./server/cmd/ui-manifest -dist $(WEB_DIR)/dist -build-version "$(VERSION)" -min-server-build "$(MIN_SERVER_BUILD)"

build: build-web
	$(GO) build -ldflags "-X main.version=$(VERSION) -X mindfs/internal/deploy.Prefix=$(MIND_FS_BASE)" -o mindfs ./cli/cmd

# 覆盖复制，不先 rm -rf：哈希 chunk 对浏览器是 immutable，删掉等于让已打开的标签页
# 在懒加载时 404。磁盘由 WEB_ASSET_TTL_DAYS 的按龄清理兜住。
# 用 -mmin 而不是 -mtime：-mtime 向下取整，`-mtime +1` 实际要到 48 小时才删。
# 分钟字面量必须等于 TTL_DAYS × 1440，由 web/tests/deploy-web-assets.test.mjs 钉住。
# 注意结尾的 `/.`：web/ 已存在时 `cp -R dist web` 会变成 web/dist。
install: build
	install -d "$(PREFIX)/bin"
	install -d "$(PREFIX)/share/mindfs"
	install -m 0755 mindfs "$(PREFIX)/bin/mindfs"
	install -m 0644 agents.json "$(PREFIX)/share/mindfs/agents.json"
	install -d "$(PREFIX)/share/mindfs/web"
	cp -R "$(WEB_DIR)/dist/." "$(PREFIX)/share/mindfs/web/"
	find "$(PREFIX)/share/mindfs/web/assets" -type f -mmin +1440 -delete

uninstall:
	rm -f "$(PREFIX)/bin/mindfs"
	rm -rf "$(PREFIX)/share/mindfs"

start:
	$(GO) run ./cli/cmd -addr $(ADDR) $(ROOT)

start-server:
	$(GO) run ./server/cmd/mindfs-server -addr $(ADDR)

test:
	$(GO) test ./...

# web 契约测试：源码守卫（tests/*.test.mjs），不需要浏览器。
# 先 typecheck 再跑测试 —— vite build 用 esbuild 不做类型检查，import 改错的唯一
# 自动防线就是 tsc，必须进这道门。
test-web:
	cd $(WEB_DIR) && $(NPM) run typecheck
	cd $(WEB_DIR) && $(NPM) test

# 安卓壳的纯逻辑单测（ReplyAlertPlanner / ReplyStateMerger）。不进 make test：
# 它要拉起 Gradle daemon，与每次 ship 的快路径无关。
# 与 build-android / android-verify 保持同一套环境：JAVA_HOME（ANDROID_GRADLE_ENV）与
# Android SDK 缺一不可，否则 Gradle 会在配置阶段报一个跟被测代码无关的错。
test-android:
	@test -n "$(ANDROID_SDK_ROOT)$(ANDROID_HOME)" || (echo "Error: ANDROID_SDK_ROOT/ANDROID_HOME is required" >&2; exit 1)
	cd $(ANDROID_DIR) && $(ANDROID_GRADLE_ENV) ./gradlew :app:testDebugUnitTest

# fork 定制覆盖率门禁：delta 文件 ⟷ docs/upstream-customizations.yaml 对账。
# 刻意不并入 `make test`——它依赖 upstream ref（无网机器应当跳过而非失败），
# 且合上游前后才是它真正要跑的时刻（见 .claude/skills/upstream-merge）。
check-upstream:
	$(GO) run ./scripts/check-upstream

# 变异账本（Plan 2026-11-20 Task 9）：逐条把 scripts/mutations.txt 里的已知错误改法
# 注入临时 worktree 并跑指定测试，测试必须变红。survived = 那行没有任何测试守着。
mutate:
	@bash scripts/mutate.sh

# ── Cross-platform distribution ──────────────────────────────────────────
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
# 前端清单里的「能服务这一版前端的后端最低版本」。缺省与构建版本相同：
# 同一提交编出来的前后端永远配套；要灰度（先发前端、后发后端）时用
# `make build-web MIN_SERVER_BUILD=v0.5.5-1200` 放宽。
MIN_SERVER_BUILD ?= $(VERSION)
DIST_DIR ?= dist
RELEASE_NOTES_FILE ?= release-notes.md
RELEASE_NOTES_LATEST_FILE ?= $(DIST_DIR)/release-notes-$(TAG).md
ANDROID_RELEASE_APK ?= $(ANDROID_DIR)/app/build/outputs/apk/release/app-release.apk
ANDROID_DIST_APK ?= $(DIST_DIR)/mindfs_$(VERSION)_android.apk
# 发布仓库身份必须与 internal/deploy.ReleaseRepo 一致（web/tests/agent9-traces.test.mjs 钉这条）。
RELEASE_REPO ?= ChanceAroundYou/mindfs
ANDROID_VERSION_URL ?= https://github.com/$(RELEASE_REPO)/releases/latest/download/android-version.json
# 曾经这里有整套 HARMONY_* / DEVECO_* 变量与 build-harmony 目标：鸿蒙 ArkTS 壳是上游带来的，
# 已连目录一起删掉（docs/upstream-customizations.yaml）。合上游时不要把它带回来。
RELEASE_ANDROID ?= 0
RELEASE_UPLOAD_JOBS ?= 4
ANDROID_JAVA_HOME ?= $(JAVA_HOME)
ANDROID_JAVA_HOME ?= $(shell if command -v /usr/libexec/java_home >/dev/null 2>&1; then /usr/libexec/java_home -v 21 2>/dev/null; fi)
ANDROID_GRADLE_ENV :=
ifneq ($(strip $(ANDROID_JAVA_HOME)),)
ANDROID_GRADLE_ENV := JAVA_HOME="$(ANDROID_JAVA_HOME)"
endif
# Windows edge-controller installer (NSIS: standard wizard + uninstaller).
# Separate from `mindfs_<TAG>_windows_amd64.zip`: that zip needs a manual unpack
# + four steps, and its `web/` never lands where the service reads it (a silent
# half install). The setup exe carries the payload, registers the S4U task, adds
# the user PATH entry and ships an uninstaller -- see
# scripts/build-windows-installer.sh.
WINDOWS_SETUP_EXE ?= $(DIST_DIR)/mindfs_$(VERSION)_windows_amd64-setup.exe
RELEASE_WINDOWS_SETUP ?= 1

RELEASE_ARTIFACTS := $(DIST_DIR)/mindfs_$(TAG)_*.tar.gz $(DIST_DIR)/mindfs_$(TAG)_*.zip $(DIST_DIR)/mindfs_$(TAG)_manifest.json
ifeq ($(RELEASE_WINDOWS_SETUP),1)
RELEASE_ARTIFACTS += $(DIST_DIR)/mindfs_$(TAG)_windows_amd64-setup.exe
endif
ifeq ($(RELEASE_ANDROID),1)
RELEASE_ARTIFACTS += $(DIST_DIR)/mindfs_$(TAG)_android.apk
RELEASE_ARTIFACTS += $(DIST_DIR)/android-version.json
endif

# Targets: OS/ARCH pairs
PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	linux/arm \
	windows/amd64 \
	windows/arm64

build-all: build-web
	@MIND_FS_BASE="$(MIND_FS_BASE)" bash scripts/build-all.sh "$(VERSION)" "$(DIST_DIR)"
ifeq ($(RELEASE_WINDOWS_SETUP),1)
	@bash scripts/build-windows-installer.sh "$(VERSION)" "$(DIST_DIR)"
endif

build-android:
	cd $(WEB_DIR) && VITE_MIND_FS_BASE=/ VITE_ANDROID_VERSION_URL="$(ANDROID_VERSION_URL)" $(NPM) run build:shell
	cd $(WEB_DIR) && node_modules/.bin/cap sync android
	cd $(ANDROID_DIR) && $(ANDROID_GRADLE_ENV) ./gradlew assembleRelease -PmindfsVersion="$(VERSION)"
	mkdir -p "$(DIST_DIR)"
	cp "$(ANDROID_RELEASE_APK)" "$(ANDROID_DIST_APK)"

# iOS 壳（iPad/iPhone）：只有 Mac 能出包，所以这条不在 make release 里，也不推给任何节点。
# 与安卓共用同一份 shell bundle（build:shell → dist-shell → capacitor.config.json 的 webDir）。
# 装机与签名三条路见 docs/ios-shell.md；在 Mac 上执行 bash scripts/build-ipa.sh 走完整链。
build-ios:
	cd $(WEB_DIR) && VITE_MIND_FS_BASE=/ $(NPM) run build:shell
	cd $(WEB_DIR) && node_modules/.bin/cap sync ios
	@echo "iOS 资源已同步到 $(IOS_DIR)/App/App/public —— 出包需在 Mac 上跑 scripts/build-ipa.sh（见 docs/ios-shell.md）"

android-doctor:
	bash scripts/android-doctor.sh

android-verify:
	@test -n "$(ANDROID_SDK_ROOT)$(ANDROID_HOME)" || (echo "Error: ANDROID_SDK_ROOT/ANDROID_HOME is required" >&2; exit 1)
	@SDK="$${ANDROID_SDK_ROOT:-$$ANDROID_HOME}"; \
		"$$SDK/build-tools/36.0.0/apksigner" verify --print-certs "$(ANDROID_DIST_APK)"; \
		sha256sum "$(ANDROID_DIST_APK)"

android-install: build-android
	@test -n "$(ANDROID_SDK_ROOT)$(ANDROID_HOME)" || (echo "Error: ANDROID_SDK_ROOT/ANDROID_HOME is required" >&2; exit 1)
	@SDK="$${ANDROID_SDK_ROOT:-$$ANDROID_HOME}"; \
		"$$SDK/platform-tools/adb" install -r "$(ANDROID_DIST_APK)"

android-version-json:
	$(GO) run ./scripts/android-version-manifest.go \
		-version "$(VERSION)" -dist "$(DIST_DIR)" -repo "$(RELEASE_REPO)" \
		-notes "$(RELEASE_NOTES_LATEST_FILE)" -apk "$(notdir $(ANDROID_DIST_APK))"

dist-clean:
	rm -rf $(DIST_DIR)

# ── Release ──────────────────────────────────────────────────────────────
# Usage: make tag TAG=v1.2.3
#
# tag 是「一次发布」的锚点：四端版本号、APK versionCode、更新清单全从它来。
# 打错了不能改（改 tag 等于让已装的机器认不出新旧），所以四件事必须在打 tag
# 之前校验，而不是留到 make release 才炸。
#
# 五条护栏只在 scripts/check-tag.sh 里有一份实现（对应 internal/deploy 的
# 用例），这里只做委托 —— 复制第二份的代价是两边会各自漂移，而这条路的失败
# 模式是「校验看起来跑过了」。
tag:
	@test -n "$(TAG)" || (echo "Usage: make tag TAG=v1.2.3" >&2; exit 1)
	@RELEASE_NOTES_FILE="$(RELEASE_NOTES_FILE)" bash scripts/check-tag.sh "$(TAG)" || exit 1
	@echo "Tagging $(TAG)"
	git push origin main
	git tag $(TAG)
	git push origin $(TAG)

# Usage: make publish-release-notes TAG=v1.2.3
publish-release-notes:
	@test -n "$(TAG)" || (echo "Usage: make publish-release-notes TAG=v1.2.3" >&2; exit 1)
	@test -f "$(RELEASE_NOTES_FILE)" || (echo "Error: release notes file not found: $(RELEASE_NOTES_FILE)" >&2; exit 1)
	@version="$$(sed -nE '1s/^#[[:space:]]+MindFS[[:space:]]+(v?[0-9]+(\.[0-9]+){1,3}[^[:space:]]*).*$$/\1/p' "$(RELEASE_NOTES_FILE)")"; \
		test "$$version" = "$(TAG)" || (echo "Error: $(RELEASE_NOTES_FILE) first line version '$$version' does not match TAG '$(TAG)'." >&2; exit 1)
	git add "$(RELEASE_NOTES_FILE)"
	@if git diff --cached --quiet -- "$(RELEASE_NOTES_FILE)"; then \
		echo "No release notes changes to commit."; \
	else \
		git commit -m "update release notes"; \
		git push origin main; \
	fi

# Usage: make verify-release TAG=v1.2.3
verify-release:
	@test -n "$(TAG)" || (echo "Usage: make verify-release TAG=v1.2.3" >&2; exit 1)
	@test -n "$(MINDFS_RELEASE_PUBLIC_KEY)" || (echo "Error: MINDFS_RELEASE_PUBLIC_KEY is required to verify release manifests." >&2; exit 1)
	@$(GO) run scripts/sign-release-manifest.go -verify -version "$(TAG)" -dist "$(DIST_DIR)" -repo "$(RELEASE_REPO)" -public-key "$(MINDFS_RELEASE_PUBLIC_KEY)"

# Usage: make release TAG=v1.2.3 [RELEASE_ANDROID=1]
# Builds desktop/server platforms and creates a GitHub release.
release:
	@command -v gh >/dev/null 2>&1 || (echo "Error: gh (GitHub CLI) is required. https://cli.github.com" >&2; exit 1)
	@test -n "$(TAG)" || (echo "Usage: make release TAG=v1.2.3 [RELEASE_ANDROID=1]" >&2; exit 1)
	@test -f "$(RELEASE_NOTES_FILE)" || (echo "Error: release notes file not found: $(RELEASE_NOTES_FILE)" >&2; exit 1)
	@test -n "$(MINDFS_RELEASE_PUBLIC_KEY)" || (echo "Error: MINDFS_RELEASE_PUBLIC_KEY is required for signed auto-update builds." >&2; exit 1)
	@if [ -z "$$MINDFS_RELEASE_PRIVATE_KEY" ] && [ -z "$$MINDFS_RELEASE_PRIVATE_KEY_FILE" ]; then \
		echo "Error: MINDFS_RELEASE_PRIVATE_KEY or MINDFS_RELEASE_PRIVATE_KEY_FILE is required to sign release manifests." >&2; \
		exit 1; \
	fi
	@version="$$(sed -nE '1s/^#[[:space:]]+MindFS[[:space:]]+(v?[0-9]+(\.[0-9]+){1,3}[^[:space:]]*).*$$/\1/p' "$(RELEASE_NOTES_FILE)")"; \
		test "$$version" = "$(TAG)" || (echo "Error: $(RELEASE_NOTES_FILE) first line version '$$version' does not match TAG '$(TAG)'." >&2; exit 1)
	$(MAKE) dist-clean
	mkdir -p "$(DIST_DIR)"
	@awk 'NR > 1 && /^# MindFS[[:space:]]+/ { exit } { print }' "$(RELEASE_NOTES_FILE)" > "$(RELEASE_NOTES_LATEST_FILE)"
	MINDFS_RELEASE_PUBLIC_KEY="$(MINDFS_RELEASE_PUBLIC_KEY)" $(MAKE) build-all VERSION="$(TAG)"
	@if [ "$(RELEASE_ANDROID)" = "1" ]; then \
		$(MAKE) build-android VERSION="$(TAG)"; \
		$(MAKE) android-version-json VERSION="$(TAG)" DIST_DIR="$(DIST_DIR)" RELEASE_NOTES_LATEST_FILE="$(RELEASE_NOTES_LATEST_FILE)"; \
	else \
		echo "Skipping Android release. Use RELEASE_ANDROID=1 to include the APK."; \
	fi
	@$(GO) run scripts/sign-release-manifest.go -version "$(TAG)" -dist "$(DIST_DIR)" -repo "$(RELEASE_REPO)"
	$(MAKE) verify-release TAG="$(TAG)" MINDFS_RELEASE_PUBLIC_KEY="$(MINDFS_RELEASE_PUBLIC_KEY)"
	@echo "Creating draft GitHub release $(TAG)"
	gh release create $(TAG) \
		--draft \
		--title "$(TAG)" \
		--notes-file "$(RELEASE_NOTES_LATEST_FILE)"
	@echo "Uploading release artifacts with $(RELEASE_UPLOAD_JOBS) parallel jobs"
	@set -- $(RELEASE_ARTIFACTS); \
		for artifact do \
			test -f "$$artifact" || { echo "Error: release artifact not found: $$artifact" >&2; exit 1; }; \
		done; \
		printf '%s\n' "$$@" | xargs -n 1 -P "$(RELEASE_UPLOAD_JOBS)" gh release upload "$(TAG)"
	@echo "Publishing GitHub release $(TAG)"
	gh release edit "$(TAG)" --draft=false
	$(MAKE) publish-release-notes TAG="$(TAG)"
