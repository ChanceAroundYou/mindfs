package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/internal/configfile"
)

// 节点名的两条入口，都只服务安装向导（NSIS 的节点名页面）：
//
//	--check-node-name <name>   本地格式校验 + 主控查重，结果写 JSON 并给退出码
//	--set-node-name <name>     把名字写进 config.json（node.name / control_sync.instance_id）
//
// 为什么要单独走 exe 而不是让 NSIS 自己发 HTTP：查重规则必须只有一份。
// 服务端 /api/nodes/validate-name 是权威，安装器、设置页、配对页都调它；
// 在 .nsi 里重写一遍 URL 拼接与错误分类，就会出现「向导说可用、配对时报重名」。
//
// 退出码是给 NSIS 判的，比解析 JSON 稳定：
//
//	0  可用
//	10 重名
//	11 格式不合法
//	12 主控不可达（名字本身没问题，向导据此询问是否继续）
const (
	nodeNameExitOK          = 0
	nodeNameExitTaken       = 10
	nodeNameExitInvalid     = 11
	nodeNameExitUnreachable = 12
)

// nodeNameMaxRunes 与节点表/中继名的可读上限一致：太长的名字在窄侧栏里退化成省略号，
// 而它同时是 URL 段与同步身份，改起来要动数据，所以入口就拦住。
const nodeNameMaxRunes = 32

// nodeNameResult 是 --check-node-name 的输出，字段名给 NSIS 的 JSON 读取与日志看。
type nodeNameResult struct {
	Name      string `json:"name"`
	Valid     bool   `json:"valid"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Deferred 表示没有可用的主控地址，只做了本地校验 —— 重名留给配对时判定。
	Deferred bool   `json:"deferred,omitempty"`
	Primary  string `json:"primary,omitempty"`
}

// nodeNameClient 是查重的 HTTP 接缝：测试用 httptest，生产用带超时的 client。
type nodeNameClient struct {
	http *http.Client
}

func runNodeNameCommand(args []string) (bool, error) {
	return runNodeNameCommandWith(args, os.Stdout, defaultNodeNameClient())
}

func defaultNodeNameClient() nodeNameClient {
	return nodeNameClient{http: &http.Client{Timeout: 8 * time.Second}}
}

func runNodeNameCommandWith(args []string, stdout io.Writer, client nodeNameClient) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "--check-node-name":
		return true, runCheckNodeName(args[1:], stdout, client)
	case "--set-node-name":
		return true, runSetNodeName(args[1:], stdout)
	default:
		return false, nil
	}
}

// ValidateNodeName 只做本地规则：非空、长度、不含控制字符与路径分隔符。
// 大小写与空白的归一化交给主控（它是唯一权威），本地只挡住明显写不进配置的值。
func ValidateNodeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("节点名称不能为空")
	}
	if len([]rune(trimmed)) > nodeNameMaxRunes {
		return "", fmt.Errorf("节点名称最多 %d 个字符", nodeNameMaxRunes)
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("节点名称不能包含控制字符")
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return "", fmt.Errorf("节点名称不能包含字符 %q", r)
		}
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "local", "home", "primary", "control", "worker", "mindfs":
		return "", fmt.Errorf("%q 是保留名称，请换一个", trimmed)
	}
	return trimmed, nil
}

func runCheckNodeName(args []string, stdout io.Writer, client nodeNameClient) error {
	flags := flag.NewFlagSet("check-node-name", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	nameFlag := flags.String("name", "", "node name to validate")
	primaryFlag := flags.String("primary", "", "primary control URL; defaults to control_sync.primary_url")
	outFlag := flags.String("out", "", "write the JSON result to this file (installer passes a temp path)")
	configFlag := flags.String("config", "", "mindfs config.json; defaults to the standard path")
	if err := flags.Parse(args); err != nil {
		return errors.New("usage: mindfs --check-node-name <name> [--primary URL] [--out FILE]")
	}
	name := strings.TrimSpace(*nameFlag)
	if name == "" && flags.NArg() == 1 {
		name = strings.TrimSpace(flags.Arg(0))
	}
	if name == "" {
		return errors.New("usage: mindfs --check-node-name <name> [--primary URL] [--out FILE]")
	}

	result, code := checkNodeName(context.Background(), name, strings.TrimSpace(*primaryFlag), effectiveConfigPath(*configFlag), client)

	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if *outFlag != "" {
		if err := writeNodeNameResult(*outFlag, encoded); err != nil {
			return err
		}
		// 纯文本副本给安装器：NSIS 里解析 JSON 等于再写一个解析器，
		// 而向导只需要把「为什么不行」原样显示出来。
		_ = writeNodeNameResult(*outFlag+".txt", []byte(result.Reason))
	}
	if stdout != nil {
		_, _ = fmt.Fprintln(stdout, string(encoded))
	}
	if code != nodeNameExitOK {
		os.Exit(code)
	}
	return nil
}

// writeNodeNameResult 用临时文件+换名：NSIS 在写的同时就读它，读到半截 JSON 会把
// 「可用」误判成「不可达」，而重新点下一步是用户在等。
func writeNodeNameResult(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".node-name-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func checkNodeName(ctx context.Context, rawName, primaryFlag, configPath string, client nodeNameClient) (nodeNameResult, int) {
	result := nodeNameResult{Name: strings.TrimSpace(rawName)}
	name, err := ValidateNodeName(rawName)
	if err != nil {
		result.Reason = err.Error()
		return result, nodeNameExitInvalid
	}
	result.Name = name
	result.Valid = true

	primary := primaryFlag
	if primary == "" && configPath != "" {
		primary = primaryFromConfig(configPath)
	}
	if primary == "" {
		// 边缘首次安装时还没有主控地址：本地规则通过就算过，重名在配对时判定。
		result.Available = true
		result.Deferred = true
		result.Reason = "尚未配置主控，重名检查将在配对时进行"
		return result, nodeNameExitOK
	}
	result.Primary = primary

	available, reason, err := queryPrimaryName(ctx, client, primary, name)
	if err != nil {
		result.Reason = fmt.Sprintf("无法连接主控：%v", err)
		return result, nodeNameExitUnreachable
	}
	result.Available = available
	result.Reason = reason
	if !available {
		return result, nodeNameExitTaken
	}
	return result, nodeNameExitOK
}

// primaryFromConfig 读 control_sync.primary_url。读不到、文件不存在都当「没配」——
// 安装向导不能因为一份半写的 config.json 而拒绝用户继续。
func primaryFromConfig(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	cs, _ := doc["control_sync"].(map[string]any)
	value, _ := cs["primary_url"].(string)
	return strings.TrimSpace(value)
}

// queryPrimaryName 调主控的 /api/nodes/validate-name。
//
// 该端点已经是「安装向导、设置页、CLI 共用」的权威入口（见 handleNodesValidateName），
// 这里只负责把它拼到带部署前缀的 base 上，并把三种结果翻成退出码语义。
func queryPrimaryName(ctx context.Context, client nodeNameClient, primary, name string) (bool, string, error) {
	endpoint, err := joinPrimaryPath(primary, "/api/nodes/validate-name")
	if err != nil {
		return false, "", err
	}
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.http.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("主控返回 HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return false, "", fmt.Errorf("主控返回了无法解析的响应: %w", err)
	}
	return parsed.Available, parsed.Reason, nil
}

// joinPrimaryPath 把 /api/... 接到主控基址上，保留它已有的部署前缀。
//
// 主控可能是 https://host/mindfs：直接 url.ResolveReference("/api/...") 会把 /mindfs
// 丢掉，请求打到根路径上得到 404，表现成「主控不可达」。
func joinPrimaryPath(primary, path string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(primary))
	if err != nil {
		return "", fmt.Errorf("主控地址无效: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", errors.New("主控地址必须是 https://host[/prefix]")
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

// runSetNodeName 把向导里选定的名字落进 config.json。
//
// 写两处：node.name 是展示名，control_sync.instance_id 是边缘的同步身份（首次为空时）。
// 只写 node.name 会让 id 退回主机名，主控上的边缘记录与界面显示就对不上。
func runSetNodeName(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("set-node-name", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	nameFlag := flags.String("name", "", "node name")
	configFlag := flags.String("config", "", "mindfs config.json; defaults to the standard path")
	if err := flags.Parse(args); err != nil {
		return errors.New("usage: mindfs --set-node-name <name> [--config FILE]")
	}
	name := strings.TrimSpace(*nameFlag)
	if name == "" && flags.NArg() == 1 {
		name = strings.TrimSpace(flags.Arg(0))
	}
	if name == "" {
		return errors.New("usage: mindfs --set-node-name <name> [--config FILE]")
	}
	clean, err := ValidateNodeName(name)
	if err != nil {
		return err
	}
	path := effectiveConfigPath(*configFlag)
	if path == "" {
		return errors.New("找不到 config.json 路径")
	}
	if err := configfile.Update(path, func(doc map[string]any) error {
		node, _ := doc["node"].(map[string]any)
		if node == nil {
			node = map[string]any{}
			doc["node"] = node
		}
		node["name"] = clean
		cs, _ := doc["control_sync"].(map[string]any)
		if cs == nil {
			cs = map[string]any{}
			doc["control_sync"] = cs
		}
		if existing, _ := cs["instance_id"].(string); strings.TrimSpace(existing) == "" {
			cs["instance_id"] = clean
		}
		return nil
	}); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "node name set to %q in %s\n", clean, path)
	}
	return nil
}
