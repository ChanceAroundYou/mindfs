package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mindfs/internal/deploy"
)

// Windows 安装版的自动更新走安装包，不走「换 zip 里的二进制」。
// 这一组钉三件事：选的是哪个产物、落点对不对、失败时不启动安装程序。

func withRuntimeGOOS(t *testing.T, value string) {
	t.Helper()
	old := runtimeGOOS
	runtimeGOOS = value
	t.Cleanup(func() { runtimeGOOS = old })
}

// installedService 造一个「装在 <tmp>/Programs/mindfs/bin/mindfs.exe」的服务。
func installedService(t *testing.T) (*Service, string) {
	t.Helper()
	prefix := filepath.Join(t.TempDir(), "Programs", "mindfs")
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(binDir, "mindfs.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewService(testReleaseRepo, "v1.2.2", exe, nil, time.Hour), prefix
}

func TestUpdateAssetSuffixFollowsLayout(t *testing.T) {
	withRuntimeGOOS(t, "windows")
	installed, _ := installedService(t)
	if got := installed.updateAssetSuffix(); got != "-setup.exe" {
		t.Fatalf("安装版后缀 = %q，期望 -setup.exe：服务自己解包在计划任务的 job 里活不下来", got)
	}

	portable := NewService(testReleaseRepo, "v1.2.2", filepath.Join(t.TempDir(), "mindfs.exe"), nil, time.Hour)
	if got := portable.updateAssetSuffix(); got != ".zip" {
		t.Fatalf("portable 后缀 = %q，期望 .zip", got)
	}

	withRuntimeGOOS(t, "linux")
	if got := installed.updateAssetSuffix(); got != ".tar.gz" {
		t.Fatalf("非 Windows 后缀 = %q，期望 .tar.gz", got)
	}
}

func TestPickAssetUsesInstallerNameOnInstalledWindows(t *testing.T) {
	withRuntimeGOOS(t, "windows")
	service, _ := installedService(t)

	want := fmt.Sprintf("mindfs_v1.2.3_windows_%s-setup.exe", runtime.GOARCH)
	release := latestRelease{Assets: []releaseAsset{{Name: want, BrowserDownloadURL: "https://example/" + want}}}
	asset, err := service.pickAsset(release, "v1.2.3")
	if err != nil {
		t.Fatalf("pickAsset() error = %v", err)
	}
	if asset.Name != want {
		t.Fatalf("pickAsset() = %q, want %q", asset.Name, want)
	}

	// 只有 zip 时不能被当成安装包：那会把更新引到根本没有的文件上。
	release.Assets[0].Name = fmt.Sprintf("mindfs_v1.2.3_windows_%s.zip", runtime.GOARCH)
	if _, err := service.pickAsset(release, "v1.2.3"); err == nil {
		t.Fatal("安装版不该接受 zip 产物")
	}
}

func TestIsInstallerAsset(t *testing.T) {
	cases := map[string]bool{
		"mindfs_v0.5.5-1700_windows_amd64-setup.exe": true,
		"MINDfs_V1_WINDOWS_AMD64-SETUP.EXE":          true,
		"mindfs_v1.2.3_windows_amd64.zip":            false,
		"mindfs_v1.2.3_linux_amd64.tar.gz":           false,
	}
	for name, want := range cases {
		if got := isInstallerAsset(name); got != want {
			t.Fatalf("isInstallerAsset(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestStageInstallerUpdateStagesVerifiedPackage(t *testing.T) {
	withRuntimeGOOS(t, "windows")
	service, prefix := installedService(t)

	payload := []byte("MZ fake installer")
	sum := sha256.Sum256(payload)
	name := "mindfs_v1.2.3_windows_amd64-setup.exe"
	url := "https://github.com/" + testReleaseRepo + "/releases/download/v1.2.3/" + name
	service.client = &http.Client{Transport: staticTransport{url: payload}}

	restoreRun := runUpdateTask
	defer func() { runUpdateTask = restoreRun }()
	launched := 0
	runUpdateTask = func() error { launched++; return nil }

	exited := make(chan int, 1)
	service.exitProcess = func(code int) { exited <- code }
	service.exitDelay = time.Millisecond

	if err := service.stageInstallerUpdate(context.Background(),
		releaseAsset{Name: name, BrowserDownloadURL: url},
		manifestArtifact{Name: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))},
	); err != nil {
		t.Fatalf("stageInstallerUpdate() error = %v", err)
	}

	staging := deploy.SetupUpdateStagingPath(prefix)
	got, err := os.ReadFile(staging)
	if err != nil {
		t.Fatalf("安装包没有落到固定路径 %s: %v", staging, err)
	}
	if string(got) != string(payload) {
		t.Fatal("落盘内容与下载内容不一致")
	}
	if _, err := os.Stat(staging + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part 临时文件没有被改名掉")
	}
	if launched != 1 {
		t.Fatalf("更新任务被调用 %d 次，期望 1 次", launched)
	}
	if status := service.GetStatus(); status.Status != "restarting" {
		t.Fatalf("状态 = %q，期望 restarting", status.Status)
	}
	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("退出码 = %d，期望 0", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("启动安装程序后没有交出进程：安装包会等一个永远不退出的旧进程")
	}
}

func TestStageInstallerUpdateRejectsBadHash(t *testing.T) {
	withRuntimeGOOS(t, "windows")
	service, prefix := installedService(t)

	name := "mindfs_v1.2.3_windows_amd64-setup.exe"
	url := "https://github.com/" + testReleaseRepo + "/releases/download/v1.2.3/" + name
	service.client = &http.Client{Transport: staticTransport{url: []byte("payload")}}

	restoreRun := runUpdateTask
	defer func() { runUpdateTask = restoreRun }()
	launched := 0
	runUpdateTask = func() error { launched++; return nil }

	err := service.stageInstallerUpdate(context.Background(),
		releaseAsset{Name: name, BrowserDownloadURL: url},
		manifestArtifact{Name: name, SHA256: strings.Repeat("0", 64), Size: 7},
	)
	if err == nil {
		t.Fatal("sha256 不匹配时必须失败")
	}
	if launched != 0 {
		t.Fatal("校验失败还启动了安装程序：等于执行未验证的二进制")
	}
	staging := deploy.SetupUpdateStagingPath(prefix)
	if _, statErr := os.Stat(staging); !os.IsNotExist(statErr) {
		t.Fatal("校验失败的安装包残留在落点上，会被下一次更新任务直接执行")
	}
}

func TestStageInstallerUpdateReportsLaunchFailure(t *testing.T) {
	withRuntimeGOOS(t, "windows")
	service, prefix := installedService(t)

	payload := []byte("MZ fake installer")
	sum := sha256.Sum256(payload)
	name := "mindfs_v1.2.3_windows_amd64-setup.exe"
	url := "https://github.com/" + testReleaseRepo + "/releases/download/v1.2.3/" + name
	service.client = &http.Client{Transport: staticTransport{url: payload}}

	restoreRun := runUpdateTask
	defer func() { runUpdateTask = restoreRun }()
	runUpdateTask = func() error { return fmt.Errorf("schtasks /run /tn mindfs-update failed") }

	exited := make(chan int, 1)
	service.exitProcess = func(code int) { exited <- code }
	service.exitDelay = time.Millisecond

	err := service.stageInstallerUpdate(context.Background(),
		releaseAsset{Name: name, BrowserDownloadURL: url},
		manifestArtifact{Name: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))},
	)
	if err == nil {
		t.Fatal("更新任务起不来时必须报错")
	}
	if !strings.Contains(err.Error(), deploy.SetupUpdateStagingPath(prefix)) {
		t.Fatalf("错误信息必须给出可以手动运行的安装包路径，实际: %v", err)
	}
	select {
	case <-exited:
		t.Fatal("更新任务没起来时不能退出进程：服务会就此消失且没人拉起")
	case <-time.After(100 * time.Millisecond):
	}
}

// 端到端：installUpdate 在安装版 Windows 上必须走安装包分支（而不是把 setup.exe 当 zip 解包）。
func TestInstallUpdateRoutesToInstaller(t *testing.T) {
	withRuntimeGOOS(t, "windows")
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	restoreReleasePublicKey(t, base64.StdEncoding.EncodeToString(publicKey))

	service, prefix := installedService(t)

	payload := []byte("MZ fake installer")
	sum := sha256.Sum256(payload)
	name := fmt.Sprintf("mindfs_v1.2.3_windows_%s-setup.exe", runtime.GOARCH)
	assetURL := "https://github.com/" + testReleaseRepo + "/releases/download/v1.2.3/" + name
	manifestURL := "https://github.com/" + testReleaseRepo + "/releases/download/v1.2.3/mindfs_v1.2.3_manifest.json"
	notesURL := "https://raw.githubusercontent.com/" + testReleaseRepo + "/main/release-notes.md"

	manifestPayload := []byte(`{"version":"v1.2.3","repo":"` + testReleaseRepo + `","artifacts":[{"name":"` +
		name + `","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + fmt.Sprint(len(payload)) + `}]}` + "\n")
	service.client = &http.Client{Transport: staticTransport{
		notesURL:    []byte("# MindFS v1.2.3\n\n## 修复\n"),
		manifestURL: signedManifestBody(t, manifestPayload, ed25519.Sign(privateKey, manifestPayload)),
		assetURL:    payload,
	}}

	restoreRun := runUpdateTask
	defer func() { runUpdateTask = restoreRun }()
	launched := 0
	runUpdateTask = func() error { launched++; return nil }
	exited := make(chan int, 1)
	service.exitProcess = func(code int) { exited <- code }
	service.exitDelay = time.Millisecond

	if err := service.installUpdate(context.Background(), "v1.2.3", false); err != nil {
		t.Fatalf("installUpdate() error = %v", err)
	}
	if launched != 1 {
		t.Fatalf("更新任务被调用 %d 次，期望 1 次：说明没有走安装包分支", launched)
	}
	if _, err := os.Stat(deploy.SetupUpdateStagingPath(prefix)); err != nil {
		t.Fatalf("安装包没有落盘: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("没有交出进程")
	}
}
