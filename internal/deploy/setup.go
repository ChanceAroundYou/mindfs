package deploy

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// SetupTrailerMagic 是「这个 exe 是自己人」的唯一凭证，安装器从文件尾部读它。
//
// 没有它，双击一个普通 mindfs.exe 会被当成安装包：解包失败、走到某个半截分支，
// 用户看到的是「双击了但什么都没发生」。
const SetupTrailerMagic uint32 = 0x4D465353 // "MFSS"

// SetupTrailerSize = 载荷长度(8 字节大端) + 魔数(4 字节)。
const SetupTrailerSize = 12

// SetupTaskName 是计划任务名，也是托盘「重启以应用配对」里写死的那个。
const SetupTaskName = "mindfs"

// AppendSetupPayload 把 [桩程序][载荷][长度+魔数] 拼成一个自解包 exe。
//
// 为什么用「追加」而不是往 PE 的某个段里塞：PE 加载器只按头部描述的节读，
// 尾部多余字节对它是透明的 —— 这就是所有自解包安装器的做法。反过来，
// 想在 Linux 上改 PE 节表需要一整套 PE 工具链，为一个安装器不值当。
func AppendSetupPayload(stubPath, dst string, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("setup payload is empty")
	}
	stub, err := os.ReadFile(stubPath)
	if err != nil {
		return err
	}
	if len(stub) == 0 {
		return fmt.Errorf("installer stub %s is empty", stubPath)
	}
	var trailer [SetupTrailerSize]byte
	binary.BigEndian.PutUint64(trailer[:8], uint64(len(payload)))
	binary.BigEndian.PutUint32(trailer[8:], SetupTrailerMagic)

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	for _, chunk := range [][]byte{stub, payload, trailer[:]} {
		if _, err := out.Write(chunk); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// ReadSetupPayload 从自己的可执行文件里切出载荷区间。
//
// 收 io.ReaderAt 而不是路径：进程读自己时文件是打开着的（Windows 独占），
// 而 io.NewSectionReader 不需要第二份句柄。
func ReadSetupPayload(r io.ReaderAt, size int64) (io.ReaderAt, int64, error) {
	if size < int64(SetupTrailerSize) {
		return nil, 0, errors.New("file too small to be a mindfs setup")
	}
	var trailer [SetupTrailerSize]byte
	if _, err := r.ReadAt(trailer[:], size-int64(SetupTrailerSize)); err != nil {
		return nil, 0, err
	}
	if binary.BigEndian.Uint32(trailer[8:]) != SetupTrailerMagic {
		return nil, 0, errors.New("not a mindfs setup package (missing trailer magic)")
	}
	length := int64(binary.BigEndian.Uint64(trailer[:8]))
	if length <= 0 || length > size-int64(SetupTrailerSize) {
		return nil, 0, fmt.Errorf("payload length %d does not fit in %d bytes", length, size)
	}
	offset := size - int64(SetupTrailerSize) - length
	return io.NewSectionReader(r, offset, length), length, nil
}

// SetupPaths 是一次安装涉及的全部落地位置。
//
// 形状必须与 server/internal/update/service.go 的 installLayout 对齐：
// 可执行文件父目录名是 bin 才算 installed，静态资源读 <prefix>/share/mindfs/web。
// 装成别的形状（比如 bin 直接放在 Programs\mindfs 下）的后果是二进制起得来、
// 但页面 404、agents.json 也读不到 —— 而且没有任何报错。
type SetupPaths struct {
	Prefix     string // ...\Programs\mindfs
	BinDir     string // ...\Programs\mindfs\bin
	BinaryPath string // ...\Programs\mindfs\bin\mindfs.exe
	ShareDir   string // ...\Programs\mindfs\share\mindfs
	AgentsPath string // ...\Programs\mindfs\share\mindfs\agents.json
	WebDir     string // ...\Programs\mindfs\share\mindfs\web
	ConfigPath string // ...\Roaming\mindfs\config.json
}

// SetupPathsFor 由两个用户目录推出安装布局。抽成纯函数是为了能在 Linux 上验 ——
// 路径拼错一个分量，装出来的是台「服务在跑但页面 403」的机器。
//
// localAppData = %LOCALAPPDATA%，roamingAppData = %APPDATA%（os.UserConfigDir）。
func SetupPathsFor(localAppData, roamingAppData string) (SetupPaths, error) {
	if strings.TrimSpace(localAppData) == "" {
		return SetupPaths{}, errors.New("LOCALAPPDATA is empty")
	}
	if strings.TrimSpace(roamingAppData) == "" {
		return SetupPaths{}, errors.New("APPDATA is empty")
	}
	prefix := filepath.Join(localAppData, "Programs", "mindfs")
	binDir := filepath.Join(prefix, "bin")
	shareDir := filepath.Join(prefix, "share", "mindfs")
	return SetupPaths{
		Prefix:     prefix,
		BinDir:     binDir,
		BinaryPath: filepath.Join(binDir, "mindfs.exe"),
		ShareDir:   shareDir,
		AgentsPath: filepath.Join(shareDir, "agents.json"),
		WebDir:     filepath.Join(shareDir, "web"),
		ConfigPath: filepath.Join(roamingAppData, "mindfs", "config.json"),
	}, nil
}

// SetupConfigMutation 是安装器对 config.json 的一次写入。
type SetupConfigMutation struct {
	Path  []string
	Value any
}

// SetupConfigMutations 是安装器要写的全部键。抽成纯数据是为了能在 Linux 上验：
// 键路径写错一个字符，边控端装完就是一台「控制面在线但谁也不跟随」的孤儿节点，
// 而界面上只有一个空的配对表单，没有任何线索指向安装器。
//
//   - control_sync.role = "edge" 让这台机器进同步轴；没有 token 时进 W2 的
//     PendingPairing（服务照常起、只挂配对端点），所以装完就能在页面里填 URL。
//   - update.auto_install = true **只在这里打开**（代码默认 false）：Unix 侧静默
//     重启会停死服务，所以这个开关不属于通用推荐值，只属于「这台机器是 Windows
//     边控端」这个事实。
func SetupConfigMutations() []SetupConfigMutation {
	return []SetupConfigMutation{
		{Path: []string{"control_sync", "role"}, Value: "edge"},
		{Path: []string{"update", "auto_install"}, Value: true},
	}
}

// ApplySetupConfig 把上面那组写入落到一个已解析的 JSON 对象上。
//
// 中间层不是对象（用户手写成字符串或数组）时**拒绝**而不是覆盖：那是用户自己的
// 配置，静默改写等于把他的手写内容吞掉；而覆盖之后的下一次启动会静默回默认值。
func ApplySetupConfig(doc map[string]any) error {
	for _, m := range SetupConfigMutations() {
		if len(m.Path) == 0 {
			return errors.New("empty config path")
		}
		cur := doc
		for _, key := range m.Path[:len(m.Path)-1] {
			next, ok := cur[key]
			if !ok || next == nil {
				child := map[string]any{}
				cur[key] = child
				cur = child
				continue
			}
			child, ok := next.(map[string]any)
			if !ok {
				return fmt.Errorf("config key %q is not an object", key)
			}
			cur = child
		}
		cur[m.Path[len(m.Path)-1]] = m.Value
	}
	return nil
}

// SetupTaskOptions 是计划任务的三个变量。
type SetupTaskOptions struct {
	TaskName string // 缺省 mindfs
	UserSID  string // 运行期求出来的当前用户 SID，形如 S-1-5-21-...-1001
	Binary   string // 绝对路径
	Addr     string // 监听地址，缺省 0.0.0.0:7332
}

// BuildSetupTaskXML 生成 schtasks /create /xml 吃的那份任务定义。
//
// 逐项对齐真机取回的那份（pc-win 的 \mindfs）：
//
//   - **S4U**：S4U 不需要有人登录，BootTrigger 才能在开机时起进程。旧的
//     InteractiveToken 从 ssh 触发 /run 根本起不了进程，而脚本照样报「已重启」。
//   - **ExecutionTimeLimit PT0S**：不限时。缺这一项默认 72 小时，服务会被计划
//     任务自己砍掉。
//   - **MultipleInstancesPolicy IgnoreNew**：早一步 /run 被静默吞掉，这正是托盘
//     「重启以应用配对」必须先等旧进程退出的原因。
//   - **RestartOnFailure 3 / PT1M**：崩溃自动拉起。
//   - **StartWhenAvailable**：错过触发时间（比如睡眠）也补跑。
//   - **StopIfGoingOnBatteries=false**：笔记本插拔电源不该停服务。
func BuildSetupTaskXML(opt SetupTaskOptions) (string, error) {
	name := strings.TrimSpace(opt.TaskName)
	if name == "" {
		name = SetupTaskName
	}
	sid := strings.TrimSpace(opt.UserSID)
	if sid == "" {
		return "", errors.New("task XML needs the current user SID")
	}
	bin := strings.TrimSpace(opt.Binary)
	if bin == "" {
		return "", errors.New("task XML needs the binary path")
	}
	addr := strings.TrimSpace(opt.Addr)
	if addr == "" {
		addr = "0.0.0.0:7332"
	}
	esc := func(s string) string { return escapeXMLText(s) }
	return strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-16"?>`,
		`<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">`,
		`  <RegistrationInfo>`,
		`    <URI>\` + esc(name) + `</URI>`,
		`  </RegistrationInfo>`,
		`  <Principals>`,
		`    <Principal id="Author">`,
		`      <UserId>` + esc(sid) + `</UserId>`,
		`      <LogonType>S4U</LogonType>`,
		`    </Principal>`,
		`  </Principals>`,
		`  <Settings>`,
		`    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`    <Hidden>true</Hidden>`,
		`    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`    <RestartOnFailure>`,
		`      <Count>3</Count>`,
		`      <Interval>PT1M</Interval>`,
		`    </RestartOnFailure>`,
		`    <StartWhenAvailable>true</StartWhenAvailable>`,
		`    <IdleSettings>`,
		`      <Duration>PT10M</Duration>`,
		`      <WaitTimeout>PT1H</WaitTimeout>`,
		`      <StopOnIdleEnd>true</StopOnIdleEnd>`,
		`      <RestartOnIdle>false</RestartOnIdle>`,
		`    </IdleSettings>`,
		`    <UseUnifiedSchedulingEngine>true</UseUnifiedSchedulingEngine>`,
		`  </Settings>`,
		`  <Triggers>`,
		`    <BootTrigger />`,
		`    <LogonTrigger />`,
		`  </Triggers>`,
		`  <Actions Context="Author">`,
		`    <Exec>`,
		`      <Command>` + esc(bin) + `</Command>`,
		`      <Arguments>--internal-autostart --addr ` + esc(addr) + `</Arguments>`,
		`    </Exec>`,
		`  </Actions>`,
		`</Task>`,
		``,
	}, "\r\n"), nil
}

// SetupUpdateTaskName 是自动更新用的旁路任务名。
//
// 为什么不能由服务自己拉起安装包：计划任务把它的进程放进一个 job 对象，
// 任务实例结束时连同子孙一起终止 —— 服务一退出，它启动的 setup.exe 就跟着死，
// 而 setup.exe 又必须先等 exe 解锁才能覆盖文件。所以更新必须由**另一个**任务实例执行，
// 它跟服务不在同一个 job 里，服务可以放心退出。
//
// 这个任务在安装时随 --register-service 一起注册（那一步本来就要管理员令牌），
// 之后每次更新只需要 schtasks /run，不再碰 UAC。
const SetupUpdateTaskName = "mindfs-update"

// SetupUpdateStagingPath 是自动更新下载的安装包落点。
//
// prefix = 安装前缀（bin 的上一级）。放在 <prefix>\update 而不是 %LOCALAPPDATA%\mindfs：
// 后者是用户数据目录，卸载时勾了「删除数据」会连带清掉，而安装包属于程序。
// 固定路径是必需的 —— 任务 XML 在注册时就把这个路径写死了。
func SetupUpdateStagingPath(prefix string) string {
	return filepath.Join(prefix, "update", "mindfs-setup.exe")
}

// UpdateTaskOptions 是自动更新任务的变量。
type UpdateTaskOptions struct {
	TaskName  string // 缺省 mindfs-update
	UserSID   string // 与 SetupTaskOptions 同一个 SID
	SetupPath string // 安装包绝对路径，见 SetupUpdateStagingPath
}

// BuildUpdateTaskXML 生成自动更新任务的定义。
//
// 与服务任务的区别是有意的：
//   - **没有触发器**：只在 schtasks /run 时执行一次。挂 BootTrigger 会在每次开机
//     重跑一遍安装包（每次都要停服务、拷文件）。
//   - **ExecutionTimeLimit PT1H**：安装包不该跑过一小时，卡住就让它被杀掉，
//     否则下次更新会被 IgnoreNew 吞掉。
//   - **没有 RestartOnFailure**：更新失败不该自动重试 —— 同一个坏包重试三次
//     和重试一次的结果相同，只会多三次停服务。
//   - **RunLevel LeastPrivilege**：安装包是 per-user 的，静默模式不会弹 UAC。
func BuildUpdateTaskXML(opt UpdateTaskOptions) (string, error) {
	name := strings.TrimSpace(opt.TaskName)
	if name == "" {
		name = SetupUpdateTaskName
	}
	sid := strings.TrimSpace(opt.UserSID)
	if sid == "" {
		return "", errors.New("update task XML needs the current user SID")
	}
	setup := strings.TrimSpace(opt.SetupPath)
	if setup == "" {
		return "", errors.New("update task XML needs the installer path")
	}
	esc := func(s string) string { return escapeXMLText(s) }
	return strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-16"?>`,
		`<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">`,
		`  <RegistrationInfo>`,
		`    <URI>\` + esc(name) + `</URI>`,
		`    <Description>MindFS 自动更新：运行下载好的安装包（静默）</Description>`,
		`  </RegistrationInfo>`,
		`  <Principals>`,
		`    <Principal id="Author">`,
		`      <UserId>` + esc(sid) + `</UserId>`,
		`      <LogonType>S4U</LogonType>`,
		`      <RunLevel>LeastPrivilege</RunLevel>`,
		`    </Principal>`,
		`  </Principals>`,
		`  <Settings>`,
		`    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`    <ExecutionTimeLimit>PT1H</ExecutionTimeLimit>`,
		`    <Hidden>true</Hidden>`,
		`    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`    <StartWhenAvailable>false</StartWhenAvailable>`,
		`    <IdleSettings>`,
		`      <StopOnIdleEnd>false</StopOnIdleEnd>`,
		`      <RestartOnIdle>false</RestartOnIdle>`,
		`    </IdleSettings>`,
		`    <UseUnifiedSchedulingEngine>true</UseUnifiedSchedulingEngine>`,
		`  </Settings>`,
		`  <Triggers />`,
		`  <Actions Context="Author">`,
		`    <Exec>`,
		`      <Command>` + esc(setup) + `</Command>`,
		`      <Arguments>/S</Arguments>`,
		`    </Exec>`,
		`  </Actions>`,
		`</Task>`,
		``,
	}, "\r\n"), nil
}

// EncodeUTF16LEFile 把文本编成带 BOM 的 UTF-16LE 字节流。
//
// schtasks /create /xml 只吃 UTF-16：写成 UTF-8 会报「任务 XML 无效」，
// 而错误信息不会告诉你编码错了。
func EncodeUTF16LEFile(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 2+len(units)*2)
	out[0], out[1] = 0xFF, 0xFE
	for i, u := range units {
		out[2+i*2] = byte(u)
		out[3+i*2] = byte(u >> 8)
	}
	return out
}

func escapeXMLText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MergeUserPath 把 dir 并进用户 PATH 字符串，返回新值与是否追加了。
//
// 这里错一个字节就是在改用户的全局 PATH：Windows 的 PATH 用 `;` 分隔、
// 比较**不区分大小写**（同一个目录写两次不会加倍但不该产生两条），
// 尾部的 `\` 不参与比较。做成纯函数是为了能把这几条逐一钉住时分毫不差。
func MergeUserPath(current, dir string) (string, bool) {
	target := strings.TrimSpace(dir)
	if target == "" {
		return current, false
	}
	want := pathKey(target)
	segments := make([]string, 0, 8)
	seen := map[string]bool{}
	present := false
	for _, seg := range strings.Split(current, ";") {
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" {
			continue
		}
		key := pathKey(trimmed)
		if seen[key] {
			continue // 用户 PATH 里的重复条目顺手收掉，第二次写入不再留
		}
		seen[key] = true
		if key == want {
			present = true
		}
		segments = append(segments, trimmed)
	}
	if present {
		return strings.Join(segments, ";"), false
	}
	return strings.Join(append(segments, target), ";"), true
}

// pathKey 是 PATH 条目的比较键：去尾部分隔符、折叠成小写。
func pathKey(seg string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(seg), `\/`))
}

// BuildSetupPayload 把一次构建的产物打成载荷 zip。
//
// 顺序是「先校验、后打包」：安装器只有一次机会，装出一个「二进制在、页面 404」
// 或「没有 agents.json 所以起不来」的半套目录，用户看到的是双击后什么都没发生，
// 而那时从一开始就该拒绝。
//
// 载荷内部相对路径与 scripts/install.ps1 从 PkgDir 里拷的路径一致，
// 只是把 `web/` 整个搬到 `share/mindfs/web` 这件事交给 setup.ps1 做。
func BuildSetupPayload(b SetupBundle) ([]byte, error) {
	if err := VerifySetupBundle(b); err != nil {
		return nil, err
	}
	layout := b.Layout
	if layout == (SetupBundleLayout{}) {
		layout = DefaultSetupBundleLayout()
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name, path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(0o644)
		entry, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	for _, e := range []struct{ name, path string }{
		{layout.Binary, filepath.Join(b.PkgDir, layout.Binary)},
		{layout.AgentsJSON, filepath.Join(b.PkgDir, layout.AgentsJSON)},
		{SetupPayloadEntryName, b.PS1Path},
	} {
		if err := addFile(e.name, e.path); err != nil {
			return nil, err
		}
	}
	// web/ 逐文件写，不预先读进内存：13 MB 的资源没必要在峰值里再存一份。
	if err := writeZipTree(zw, filepath.Join(b.PkgDir, layout.WebDir), layout.WebDir); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeZipTree 是 BuildSetupPayload 需要的「只往已有的 writer 里加一棵树」。
// 单独一份而不复用 WriteZipDir：后者自己管 zipper 的 Close，
// 而这里还有三个平铺文件要先写进去。
func writeZipTree(zw *zip.Writer, root, prefix string) error {
	root = filepath.Clean(root)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to package symlink: %s", rel)
		}
		name := filepath.ToSlash(filepath.Join(prefix, rel))
		if d.IsDir() {
			_, err := zw.Create(name + "/")
			return err
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(info.Mode().Perm())
		hdr.Modified = info.ModTime()
		entry, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(entry, f)
		return err
	})
}

// SetupStubName 是桩程序在 dist 里的文件名（由 build-windows-setup.sh 交叉编译）。
const SetupStubName = "mindfs-setup-stub-windows-amd64.exe"

// SetupPayloadEntryName 是载荷里那份 .ps1 的名字。写死：改名等于让安装器
// 「解开了但什么都没执行」。
const SetupPayloadEntryName = "setup.ps1"

// SetupBundle 描述载荷里必须有的东西，以及它们从哪来。
//
// 缺任何一项都在打包时失败：装完打不开首页 / 起不来的安装包，比没有安装包更糟，
// 而且症状全都不指向打包步骤（用户看到的是双击后什么都没发生）。
type SetupBundle struct {
	PkgDir  string // dist/mindfs_<ver>_windows_amd64（构建产物）
	PS1Path string // 仓库里的 scripts/windows-setup/setup.ps1
	Layout  SetupBundleLayout
}

// SetupBundleLayout 是载荷内部的相对路径。写成结构体而不是常量，
// 是为了让测试能构造一个「错一个分量」的载荷来看守卫会不会红。
type SetupBundleLayout struct {
	Binary     string
	AgentsJSON string
	WebDir     string
}

// DefaultSetupBundleLayout 与 scripts/build-all.sh 的产物一致。
func DefaultSetupBundleLayout() SetupBundleLayout {
	return SetupBundleLayout{
		Binary:     "mindfs.exe",
		AgentsJSON: "agents.json",
		WebDir:     "web",
	}
}

// VerifySetupBundle 检查载荷完整性。web 的两个锚点与 scripts/install.ps1 同判据
// （index.html 是页面本身，favicon.svg 是静态资源的第一个请求）。
func VerifySetupBundle(b SetupBundle) error {
	if strings.TrimSpace(b.PkgDir) == "" {
		return errors.New("package dir is empty")
	}
	if strings.TrimSpace(b.PS1Path) == "" {
		return errors.New("setup.ps1 path is empty")
	}
	files := []struct{ label, path string }{
		{b.Layout.Binary, filepath.Join(b.PkgDir, b.Layout.Binary)},
		{b.Layout.AgentsJSON, filepath.Join(b.PkgDir, b.Layout.AgentsJSON)},
		{"setup.ps1", b.PS1Path},
	}
	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil {
			return fmt.Errorf("payload missing %s: %w", f.label, err)
		}
		if info.IsDir() {
			return fmt.Errorf("payload %s is a directory, want a file", f.label)
		}
	}
	webDir := filepath.Join(b.PkgDir, b.Layout.WebDir)
	for _, name := range []string{"index.html", "favicon.svg"} {
		info, err := os.Stat(filepath.Join(webDir, name))
		if err != nil {
			return fmt.Errorf("payload web assets missing %s: %w (run 'make build-web')", name, err)
		}
		if info.IsDir() {
			return fmt.Errorf("payload web/%s is a directory, want a file", name)
		}
	}
	return nil
}
