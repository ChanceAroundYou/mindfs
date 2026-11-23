package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/app"
)

// controlSyncTokenFileName 是 token_file 的缺省文件名，落在 <config-dir>/mindfs/ 下。
// 与 control_sync.db 同目录：一份是同步索引，一份是进它门的钥匙。
const controlSyncTokenFileName = "control_sync.token"

// errSyncTokenMissing 表示 token_file 不存在或内容为空 —— 两种情况对主节点是同一件事
// （该生成一份新的），对边缘端则都要报错（边缘的密钥只能由人放进去）。
var errSyncTokenMissing = errors.New("control_sync: token file missing")

// effectiveConfigPath 返回本进程实际读的 config.json 路径：显式 -config 优先，
// 否则用缺省路径。配对要把 control_sync 段写回这个文件 —— 写到别处等于
// 「配对成功」写进一份没人读的文件，重启后回到原样。
func effectiveConfigPath(explicit string) string {
	if path := strings.TrimSpace(explicit); path != "" {
		return path
	}
	path, ok := defaultStartupConfigPath()
	if !ok {
		return ""
	}
	return path
}

// controlSyncOptions 把 config.json 的 control_sync 段翻成启动参数。
//
// 两条默认行为（用户裁定）：
//   - 没写 control_sync 段时**默认启用**，只对控制面节点成立。同步搬的是整份控制面
//     （含口令 hash），worker 不持有它，开着只会得到一个永远转发的角色。
//   - role 缺省 primary。主节点的 token 由装配层在该路径上生成（首次上线不该要求
//     用户先手搓密钥）；边缘的 token 由配对流程落到 token_file，还没配上时进
//     「等配对」档（PendingPairing）—— 服务照常起、只挂配对端点，见下面的 default 分支。
func controlSyncOptions(cfg startupConfig, nodeRole string, configPath string) (*app.ControlSyncOptions, error) {
	// 这里只判断唯一需要分叉的那一档：根 module 不能 import server/internal，
	// 拿不到 nodeinfo.Normalize。语义与它一致 —— 小写去空白后是 "worker" 才是 worker，
	// 零值与未知值都按 control 处理。
	if strings.EqualFold(strings.TrimSpace(nodeRole), "worker") {
		return nil, nil
	}
	cs := cfg.ControlSync
	enabled := true
	if cs != nil && cs.Enabled != nil {
		enabled = *cs.Enabled
	}
	if !enabled {
		return nil, nil
	}
	var instanceID, primaryURL, caFile, tokenFile string
	role := "primary"
	if cs != nil {
		if v := strings.TrimSpace(stringValue(cs.Role)); v != "" {
			role = v
		}
		instanceID = strings.TrimSpace(stringValue(cs.InstanceID))
		primaryURL = strings.TrimSpace(stringValue(cs.PrimaryURL))
		caFile = strings.TrimSpace(stringValue(cs.CAFile))
		tokenFile = strings.TrimSpace(stringValue(cs.TokenFile))
	}
	if tokenFile == "" {
		dir, err := autoStartConfigDir()
		if err != nil {
			return nil, fmt.Errorf("control_sync: resolve config dir: %w", err)
		}
		tokenFile = filepath.Join(dir, controlSyncTokenFileName)
	}
	opts := &app.ControlSyncOptions{
		Enabled:    true,
		Role:       role,
		InstanceID: instanceID,
		PrimaryURL: primaryURL,
		CAFile:     caFile,
		TokenFile:  tokenFile,
		ConfigPath: configPath,
	}
	token, err := readSyncToken(tokenFile)
	switch {
	case err == nil:
		opts.Token = token
	case !errors.Is(err, errSyncTokenMissing):
		return nil, err
	default:
		// 主节点：交给装配层在同一个路径上生成。
		//
		// 边缘：**不拒绝启动**，切成「等配对」—— 那台机器上还没有 token，唯一能拿到
		// token 的路径是它自己的配对页面，而页面要先起得来。这里返回错误会把用户关在
		// 门外：服务起不来，也就没有地方填那个表单。
		if strings.EqualFold(role, "edge") {
			opts.Enabled = false
			opts.PendingPairing = true
		}
	}
	return opts, nil
}

// readSyncToken 从 token_file 读密钥。文件内容只有一个 token，允许行尾换行
// （用 `echo` 生成时几乎必然带上）。
//
// 「文件不存在」与「文件为空」都包成 errSyncTokenMissing：调用方对它们的处理
// 是同一件事，而错误文案要能指出是哪一种，否则排查时只能盯着一个路径猜。
func readSyncToken(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%w: token_file path is empty", errSyncTokenMissing)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: read token_file: %v", errSyncTokenMissing, err)
		}
		return "", fmt.Errorf("control_sync: read token_file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("%w: token_file %q is empty", errSyncTokenMissing, path)
	}
	return token, nil
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
