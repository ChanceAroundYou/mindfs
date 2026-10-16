package agent

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func probeInstallStatus(name string, def Definition, ts time.Time) Status {
	status := unavailableStatus(name, false, "", ts)
	status.Protocol = agentDefinitionProtocol(name, def)
	status.Brief = strings.TrimSpace(def.Brief)
	status.InstallCommands = append([]string(nil), def.InstallCommands...)
	status.UpdateCommands = append([]string(nil), def.UpdateCommands...)
	if def.Command == "" {
		status.ProbeError = "command required"
		return status
	}
	if _, err := exec.LookPath(def.Command); err != nil {
		status.ProbeError = err.Error()
		return status
	}
	status.Installed = true
	status.ProbeError = "probe pending"
	return status
}

func agentDefinitionProtocol(name string, def Definition) Protocol {
	protocol := def.Protocol
	if protocol == "" {
		protocol = DefaultProtocol(name)
	}
	return protocol
}

func populateProbeModels(ctx context.Context, sess agenttypes.Session, status *Status) {
	modelsCtx, modelsCancel := context.WithTimeout(ctx, probeModelListTimeout)
	defer modelsCancel()

	models, err := sess.ListModels(modelsCtx)
	if err != nil {
		status.ModelsError = err.Error()
		return
	}
	status.CurrentModelID = models.CurrentModelID
	status.Models = models.Models
	status.Efforts = inferAgentEfforts(models.Models)
	status.SupportsFastService = supportsAgentFastService(status.Name)

	modes, err := sess.ListModes(modelsCtx)
	if err != nil {
		status.ModesError = err.Error()
	} else {
		status.CurrentModeID = modes.CurrentModeID
		status.Modes = modes.Modes
	}
	if defaultsReader, ok := sess.(agenttypes.DefaultsReader); ok {
		defaults, defaultsErr := defaultsReader.RuntimeDefaults(modelsCtx)
		if defaultsErr != nil {
			log.Printf("[agent/probe] defaults.error agent=%s err=%v", status.Name, defaultsErr)
		}

		if value := strings.TrimSpace(defaults.Model); value != "" {
			status.DefaultModelID = value
		}
		if value := strings.TrimSpace(defaults.Effort); value != "" {
			status.DefaultEffort = value
		}
		status.DefaultFastService = defaults.FastService
	}
}

func populateProbeCommands(ctx context.Context, sess agenttypes.Session, status *Status) {
	commandsCtx, commandsCancel := context.WithTimeout(ctx, probeCommandListTimeout)
	defer commandsCancel()

	commands, err := sess.ListCommands(commandsCtx)
	if err != nil {
		status.CommandsError = err.Error()
		return
	}
	status.Commands = commands.Commands
}

func normalizeStatus(status Status) Status {
	status.RuntimeError = strings.TrimSpace(status.RuntimeError)
	status.ProbeError = strings.TrimSpace(status.ProbeError)
	switch {
	case status.RuntimeError != "":
		status.Error = status.RuntimeError
	case status.ProbeError != "":
		status.Error = status.ProbeError
	default:
		status.Error = strings.TrimSpace(status.Error)
	}
	return status
}

func preserveKnownCapabilities(prev Status, next Status) Status {
	if next.Available || !next.Installed {
		return next
	}
	if next.CurrentModelID == "" {
		next.CurrentModelID = prev.CurrentModelID
	}
	if next.CurrentModeID == "" {
		next.CurrentModeID = prev.CurrentModeID
	}
	if next.DefaultModelID == "" {
		next.DefaultModelID = prev.DefaultModelID
	}
	if next.DefaultEffort == "" {
		next.DefaultEffort = prev.DefaultEffort
	}
	if next.DefaultFastService == "" {
		next.DefaultFastService = prev.DefaultFastService
	}
	if !next.SupportsFastService {
		next.SupportsFastService = prev.SupportsFastService
	}
	if len(next.Efforts) == 0 {
		next.Efforts = prev.Efforts
	}
	if len(next.Models) == 0 {
		next.Models = prev.Models
	}
	if len(next.Modes) == 0 {
		next.Modes = prev.Modes
	}
	if len(next.Commands) == 0 {
		next.Commands = prev.Commands
	}
	if next.Brief == "" {
		next.Brief = prev.Brief
	}
	if len(next.InstallCommands) == 0 {
		next.InstallCommands = prev.InstallCommands
	}
	if len(next.UpdateCommands) == 0 {
		next.UpdateCommands = prev.UpdateCommands
	}
	return next
}

func inferAgentEfforts(models []agenttypes.ModelInfo) []string {
	hasSupport := false
	looksLikeClaude := false
	efforts := make([]string, 0)
	seenEfforts := map[string]struct{}{}
	appendEffort := func(effort string) {
		effort = strings.TrimSpace(effort)
		if effort == "" {
			return
		}
		if _, ok := seenEfforts[effort]; ok {
			return
		}
		seenEfforts[effort] = struct{}{}
		efforts = append(efforts, effort)
	}
	for _, model := range models {
		if !model.SupportEffort {
			continue
		}
		hasSupport = true
		for _, effort := range model.Efforts {
			appendEffort(effort)
		}
		joined := strings.ToLower(strings.TrimSpace(model.ID) + " " + strings.TrimSpace(model.Name))
		if strings.Contains(joined, "sonnet") || strings.Contains(joined, "opus") || strings.Contains(joined, "fable") || strings.Contains(joined, "haiku") {
			looksLikeClaude = true
		}
	}
	if !hasSupport {
		return nil
	}
	if len(efforts) > 0 {
		return efforts
	}
	if looksLikeClaude {
		return []string{"low", "medium", "high", "xhigh", "max"}
	}
	return []string{"low", "medium", "high", "xhigh"}
}

func supportsAgentFastService(agentName string) bool {
	return strings.TrimSpace(agentName) == "codex"
}
