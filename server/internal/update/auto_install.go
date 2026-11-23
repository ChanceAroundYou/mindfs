package update

import (
	"context"
	"log"
	"strings"
)

// 静默自动更新（W3 / D4）。
//
// 默认关闭，且**必须**默认关闭：安装完成后 installUpdate 会替换二进制并重启进程
// （Unix 走 setsid + os.Exit(0)，Windows 走 Start-Process + os.Exit(0)）。
// 在 Unix 上，systemd 单元的 Restart=on-failure 不会重拉一个退出码 0 的进程，
// 而 setsid 又让新进程脱离了原 cgroup —— 静默重启等于让服务从 systemd 眼里消失。
// 本机与 worker 上还会顺手杀掉正在跑的 claude 子进程。
//
// 所以只有 Windows 安装包（自己注册了计划任务、重启后由计划任务接管）才写
// update.auto_install=true；别的部署形态一律保持关闭。

// autoInstallDecision 是这一拍该不该静默安装的唯一判据。
//
// 抽成纯函数是为了能逐条打红：装配错一处（比如把忙判据写成「忙就装」）时，
// 表现是「用户的 agent 回合被腰斩」，现场很难回溯到这一行。
func autoInstallDecision(st Status, autoInstall, busy bool) (bool, string) {
	switch {
	case !autoInstall:
		return false, "disabled"
	case !st.AutoUpdateSupported:
		// 安装布局不支持（portable 目录、dev 构建、缺发布公钥）时装上去不会重启。
		return false, "unsupported"
	case !st.HasUpdate || strings.TrimSpace(st.LatestVersion) == "":
		return false, "no-update"
	case st.Status == "downloading" || st.Status == "installing" || st.Status == "restarting":
		// 已经有一轮在跑。重复触发只会让 TriggerUpdate 回 error 并写进状态。
		return false, "in-progress"
	case busy:
		// 有会话在回复：这一拍只报 available，下一拍再看。
		return false, "busy"
	}
	return true, "install"
}

// SetAutoInstall 开启/关闭静默安装。只由启动装配调用一次。
func (s *Service) SetAutoInstall(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.autoInstall = enabled
	s.mu.Unlock()
}

// AutoInstallEnabled 报告当前是否开着。
func (s *Service) AutoInstallEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoInstall
}

// SetBusyProbe 注入「现在有没有会话在回复」的探针。
//
// 传 nil 等于恒不忙 —— 那只该出现在测试里：生产接线的意义就是别腰斩用户的回合。
func (s *Service) SetBusyProbe(probe func() bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.busyProbe = probe
	s.mu.Unlock()
}

// setCheckProbe / setTriggerProbe 只给测试用：把网络与重启换成可数的桩。
func (s *Service) setCheckProbe(fn func(ctx context.Context)) {
	s.mu.Lock()
	s.checkNow = fn
	s.mu.Unlock()
}

func (s *Service) setTriggerProbe(fn func(ctx context.Context) error) {
	s.mu.Lock()
	s.trigger = fn
	s.mu.Unlock()
}

func (s *Service) busy() bool {
	s.mu.RLock()
	probe := s.busyProbe
	s.mu.RUnlock()
	if probe == nil {
		return false
	}
	return probe()
}

// maybeAutoInstall 在每一轮版本检查之后跑一次。
//
// 只有判据全过才真的触发；其余情况只留一行日志（每一小时一行，不是每请求）。
func (s *Service) maybeAutoInstall(ctx context.Context) {
	if s == nil {
		return
	}
	install, reason := autoInstallDecision(s.GetStatus(), s.AutoInstallEnabled(), s.busy())
	if !install {
		if reason != "disabled" && reason != "no-update" {
			log.Printf("[update] auto_install.skip reason=%s", reason)
		}
		return
	}
	s.mu.RLock()
	trigger := s.trigger
	s.mu.RUnlock()
	if trigger == nil {
		trigger = s.TriggerUpdate
	}
	log.Printf("[update] auto_install.begin latest=%s", s.GetStatus().LatestVersion)
	if err := trigger(ctx); err != nil {
		log.Printf("[update] auto_install.error err=%v", err)
	}
}
