package api

import (
	"strings"

	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/session"
)

func commandShellField(s *session.Session, resolvedShell string) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if shell := strings.TrimSpace(s.Shell); shell != "" {
		return shell
	}
	return strings.TrimSpace(resolvedShell)
}

// resolveCommandShell 算出配置里解析出的默认 shell（command 类会话没显式指定时用）。
// AppContext 侧的广播要能填 shell 字段，但那边没有 HTTPHandler，所以这个解析独立成方法。
func (s *AppContext) ResolveCommandShell() string {
	if s == nil {
		return ""
	}
	pool := s.GetAgentPool()
	if pool == nil {
		return ""
	}
	cfg := pool.Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return commandexec.ResolveShell(shells)
}

func (h *HTTPHandler) resolveCommandShell() string {
	return h.AppContext.ResolveCommandShell()
}

func (h *HTTPHandler) configuredShells() []commandexec.ShellSpec {
	if h == nil || h.AppContext == nil || h.AppContext.GetAgentPool() == nil {
		return nil
	}
	cfg := h.AppContext.GetAgentPool().Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return shells
}

func (h *HTTPHandler) commandShellForSession(s *session.Session) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if strings.TrimSpace(s.Shell) != "" {
		return strings.TrimSpace(s.Shell)
	}
	return commandexec.ResolveShell(h.configuredShells())
}

func (h *HTTPHandler) commandShellForResponse(s *session.Session, aux map[int][]session.ExchangeAux) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if shell := session.InferCommandShellFromAux(aux); strings.TrimSpace(shell) != "" {
		return strings.TrimSpace(shell)
	}
	return h.commandShellForSession(s)
}
