// ui-manifest 为前端构建产物生成 `ui-manifest.json`（边缘镜像的索引）。
//
// 为什么不用 Node 在 vite 里生成：清单是安全相关产物，边缘拿它决定激活哪一版前端
// 并逐文件校验哈希。哈希与路径校验的规则只能有一份实现，否则两份迟早漂移，
// 而漂移的表现是「边缘把每一版都判成坏包、永远停在随包旧前端」，排查起来毫无线索。
// 服务端解析清单用的是同一个 controlsync 包。
//
// 用法：
//
//	go run ./server/cmd/ui-manifest -dist web/dist -build-version v1.2.3
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mindfs/server/internal/controlsync"
)

func main() {
	dist := flag.String("dist", "web/dist", "frontend build output directory")
	buildVersion := flag.String("build-version", "", "version string recorded in the manifest")
	minServerBuild := flag.String("min-server-build", "", "minimum backend build able to serve this frontend (defaults to -build-version)")
	flag.Parse()

	if *buildVersion == "" {
		fmt.Fprintln(os.Stderr, "ui-manifest: -build-version is required")
		os.Exit(2)
	}
	manifest, err := controlsync.BuildUIManifest(*dist, *buildVersion, *minServerBuild)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ui-manifest: %v\n", err)
		os.Exit(1)
	}
	raw, err := controlsync.MarshalUIManifest(manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ui-manifest: write: %v\n", err)
		os.Exit(1)
	}
	out := filepath.Join(*dist, controlsync.UIManifestName)
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ui-manifest: write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ui-manifest: %s files=%d build=%s min_server=%s\n", out, len(manifest.Files), manifest.BuildVersion, manifest.MinServerBuild)
}
