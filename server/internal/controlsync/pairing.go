package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"mindfs/internal/brand"
	"mindfs/server/internal/config"
)

// PairRequest 是「把本机接到某个主控端」需要人提供的全部输入。
//
// 没有做自动发现（用户裁定：手动填一次 URL，之后自动）：UDP 广播在 Windows 防火墙、
// 多网段、Docker 网桥下可靠不了，而「一次填错」的代价远小于「每次启动都可能连错机器」。
type PairRequest struct {
	PrimaryURL string // 形如 https://home.xiaokubao.space/mindfs（含部署前缀）
	Token      string // 主控端 token_file 的内容
	InstanceID string // 本机在 /edges 名单里的名字，留空取主机名
	CAFile     string // 自签/内网 CA；留空用系统根
}

// PairResult 是配对成功后可展示的事实，不含 token。
type PairResult struct {
	InstanceID   string `json:"instance_id"`
	PrimaryURL   string `json:"primary_url"`
	PrimaryBuild string `json:"primary_build_version"`
	HeadRev      int64  `json:"head_rev"`
}

// 配对过程中每个网络动作的上限。配对人就坐在机器前面等结果，超过这个数说明地址或
// 证书有问题，早报错比挂着好。
const pairProbeTimeout = 10 * time.Second

// ErrPairUnreachable 把「连不上/证书不认」与「连上了但对端拒绝」分开。
// 前者是网络或 URL 写错（HTTP 502），后者是 token 或配置不对（HTTP 400），
// 界面上要给的是两条完全不同的处置建议。
var ErrPairUnreachable = errors.New("control_sync: primary unreachable")

// Pair 完成一次边控端配对：探活 → 验 token → 落 token → 改 config.json。
//
// 顺序是有意的：
//   - **先验证再落盘**。任何一步失败都不该在磁盘上留下半个配对（一个能读出来、
//     却永远同步不上的 token 文件，会把「没配对」伪装成「配对了」）。
//   - **token 先于 config**。反过来会留下「config 说已配对、token 不存在」的状态，
//     而那种状态下 cli 会直接拒绝启动 —— 界面都没了，用户只能改文件。
//
// 三种失败要分开说，因为处置完全不同：地址不通（网络/证书）、token 不对（要重新
// 拷一份）、主控端没开同步（要去主控端开）。混成一句「配对失败」等于让人猜。
func Pair(ctx context.Context, req PairRequest, tokenFile, configPath string) (PairResult, error) {
	primaryURL := strings.TrimSpace(req.PrimaryURL)
	if primaryURL == "" {
		return PairResult{}, errors.New("control_sync: primary_url is required")
	}
	if strings.TrimSpace(configPath) == "" {
		return PairResult{}, errors.New("control_sync: config path is required to persist pairing")
	}
	if strings.TrimSpace(tokenFile) == "" {
		return PairResult{}, errors.New("control_sync: token_file is required")
	}
	token := strings.TrimSpace(req.Token)
	if err := ValidateSyncToken(token); err != nil {
		return PairResult{}, err
	}
	base, err := parsePrimaryURL(primaryURL)
	if err != nil {
		return PairResult{}, err
	}
	instanceID := strings.TrimSpace(req.InstanceID)
	if instanceID == "" {
		instanceID = hostnameOrEmpty()
	}
	if instanceID == "" {
		return PairResult{}, errors.New("control_sync: instance_id is required (hostname unavailable)")
	}

	client, err := newSyncHTTPClient(strings.TrimSpace(req.CAFile))
	if err != nil {
		return PairResult{}, err
	}
	client.Timeout = pairProbeTimeout

	if err := probePrimaryHealth(ctx, client, base); err != nil {
		return PairResult{}, err
	}
	head, err := probePrimaryHead(ctx, client, base, token, instanceID)
	if err != nil {
		return PairResult{}, err
	}
	if head.SchemaVersion != SyncSchemaVersion {
		return PairResult{}, fmt.Errorf(
			"control_sync: primary speaks sync schema v%d, this build speaks v%d — upgrade the older side first",
			head.SchemaVersion, SyncSchemaVersion)
	}

	if err := WriteSyncTokenFile(tokenFile, token); err != nil {
		return PairResult{}, err
	}
	result := PairResult{
		InstanceID:   instanceID,
		PrimaryURL:   primaryURL,
		PrimaryBuild: head.BuildVersion,
		HeadRev:      head.HeadRev,
	}
	if err := config.UpdateJSONFile(configPath, func(doc map[string]any) error {
		return applyPairing(doc, result, strings.TrimSpace(req.CAFile), tokenFile)
	}); err != nil {
		return PairResult{}, err
	}
	return result, nil
}

// applyPairing 把配对结果合并进 config.json 的对象。
//
// 只碰 control_sync 这一段和「role 被写成 worker」这一种情况：用户手写的 router /
// live_compact / channels 不属于配对的职责范围，丢一个都是配置漂移。
func applyPairing(doc map[string]any, result PairResult, caFile, tokenFile string) error {
	// 边缘要出页面（主控端不可达时靠它继续用），而 role=worker 的节点根本不注入
	// 静态目录 —— 留着 worker 会让「配对成功」表现为一个 403 的空页面。
	if role, _ := doc["role"].(string); strings.EqualFold(strings.TrimSpace(role), "worker") {
		doc["role"] = "control"
	}
	section, ok := doc["control_sync"].(map[string]any)
	if !ok {
		section = map[string]any{}
		doc["control_sync"] = section
	}
	section["enabled"] = true
	section["role"] = "edge"
	section["instance_id"] = result.InstanceID
	section["primary_url"] = result.PrimaryURL
	section["token_file"] = tokenFile
	if caFile == "" {
		delete(section, "ca_file")
	} else {
		section["ca_file"] = caFile
	}
	return nil
}

// probePrimaryHealth 用 /health 判定对端到底是控制节点还是运行节点。
//
// /health 是唯一安全的探活目标：无需鉴权、永不 404，而且会如实报出 role。
// /api/node-info 在 worker 上是 403，用它探活会把每个运行节点都当成「离线」。
func probePrimaryHealth(ctx context.Context, client *http.Client, base *url.URL) error {
	endpoint := *base
	endpoint.Path = joinSyncPath(base.Path, "/health")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("control_sync: build health probe: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: reach %s: %v", ErrPairUnreachable, endpoint.String(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control_sync: %s returned %d, expected 200 (wrong URL or the primary is not running)",
			endpoint.String(), resp.StatusCode)
	}
	var health struct {
		OK   bool   `json:"ok"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		return fmt.Errorf("control_sync: %s is not a %s health payload: %w", endpoint.String(), brand.Name(), err)
	}
	if !health.OK {
		return fmt.Errorf("control_sync: %s reports unhealthy", endpoint.String())
	}
	if !strings.EqualFold(strings.TrimSpace(health.Role), "control") {
		return fmt.Errorf("control_sync: %s is role=%q, pairing requires a control node",
			endpoint.String(), strings.TrimSpace(health.Role))
	}
	return nil
}

// hostnameOrEmpty 取主机名作为缺省 instance_id。取不到就返回空串，由调用方报错 ——
// 空 instance_id 会让主控端的 /edges 名单里出现一行无名记录。
func hostnameOrEmpty() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// probePrimaryHead 用签名请求验证 token，并取回主控端的身份与 schema。
//
// 401 / 404 的文案要分别点名：前者是 token 拷错了，后者是主控端没开同步 ——
// 两者都表现为「配对失败」，但一个要去重新拷文件，一个要去主控端改配置。
func probePrimaryHead(ctx context.Context, client *http.Client, base *url.URL, token, instanceID string) (HeadInfo, error) {
	endpoint := *base
	endpoint.Path = joinSyncPath(base.Path, "/api/control-sync/head")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return HeadInfo{}, fmt.Errorf("control_sync: build head probe: %w", err)
	}
	if err := SignSyncRequest(req, token, instanceID, nil, time.Now()); err != nil {
		return HeadInfo{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return HeadInfo{}, fmt.Errorf("%w: reach %s: %v", ErrPairUnreachable, endpoint.String(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return HeadInfo{}, errors.New("control_sync: the primary rejected this token (401) — copy token_file again")
	case http.StatusNotFound:
		return HeadInfo{}, errors.New("control_sync: the primary has control sync disabled (404) — enable it there first")
	default:
		return HeadInfo{}, fmt.Errorf("control_sync: %s returned %d: %s",
			endpoint.String(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var head HeadInfo
	if err := json.Unmarshal(body, &head); err != nil {
		return HeadInfo{}, fmt.Errorf("control_sync: decode head from %s: %w", endpoint.String(), err)
	}
	return head, nil
}
