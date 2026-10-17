package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)
func probeConfiguredAgentWithPool(ctx context.Context, name string, def Definition, pool *Pool, probeSessions *probeSessionStore, phase probePhase) Status {
	status := probeInstallStatus(name, def, time.Now().UTC())
	if !status.Installed {
		return status
	}
	return probeInstalledAgentWithPool(ctx, name, def, pool, probeSessions, status, phase)
}


func probeInstalledAgentWithPool(ctx context.Context, name string, def Definition, pool *Pool, probeSessions *probeSessionStore, status Status, phase probePhase) Status {
	if !status.Installed {
		return status
	}
	status.Installed = true

	tmpRoot, err := EnsureStableWorkDir("agent-probe", name)
	if err != nil {
		status.ProbeError = err.Error()
		return status
	}

	pool, ownsPool := resolveProbePool(def, pool)
	if ownsPool {
		defer pool.CloseAll()
	}

	sessionKey := fmt.Sprintf("probe-%s", name)
	defer pool.Close(sessionKey)
	openCtx := ctx
	sessionCancel := func() {}
	if timeout, ok := probeSessionTimeoutForPhase(phase); ok {
		openCtx, sessionCancel = context.WithTimeout(ctx, timeout)
	}
	defer sessionCancel()
	openInput := agenttypes.OpenSessionInput{
		SessionKey: sessionKey,
		AgentName:  name,
		Probe:      true,
		RootPath:   tmpRoot,
	}
	resumedBinding := ProbeSessionBinding{}
	resumed := false
	if binding, ok := loadProbeSessionBinding(probeSessions, name); ok {
		if shouldRotateProbeSession(binding) {
			log.Printf("[agent/probe] rotate agent=%s phase=%s probe_count=%d threshold=%d action=open_new_runtime_session", name, phase, binding.ProbeCount, probeRotateAfterCount)
			if clearErr := clearProbeSessionBinding(probeSessions, name); clearErr != nil {
				log.Printf("[agent/probe] rotate.clear_failed agent=%s phase=%s err=%v", name, phase, clearErr)
			}
		} else {
			resumedBinding = binding
			resumed = true
		}
	}
	if resumed {
		openInput.AgentSessionID = resumedBinding.AgentSessionID
		log.Printf("[agent/probe] open agent=%s phase=%s action=resume_runtime_session agent_session_id=%s", name, phase, openInput.AgentSessionID)
	} else {
		log.Printf("[agent/probe] open agent=%s phase=%s action=open_new_runtime_session", name, phase)
	}

	sess, err := pool.GetOrCreate(openCtx, openInput)
	if err != nil && strings.TrimSpace(openInput.AgentSessionID) != "" {
		log.Printf("[agent/probe] resume.error agent=%s phase=%s agent_session_id=%s err=%v fallback=open_new_runtime_session", name, phase, openInput.AgentSessionID, err)
		if clearErr := clearProbeSessionBinding(probeSessions, name); clearErr != nil {
			log.Printf("[agent/probe] resume.clear_failed agent=%s phase=%s err=%v", name, phase, clearErr)
		}
		openInput.AgentSessionID = ""
		resumed = false
		resumedBinding = ProbeSessionBinding{}
		sess, err = pool.GetOrCreate(openCtx, openInput)
	}
	if err != nil {
		status.ProbeError = err.Error()
		return status
	}

	status.Available = true
	status.Error = ""
	status.ProbeError = ""
	if err := storeProbeSessionBinding(probeSessions, name, sess.SessionID(), resumedBinding, resumed); err != nil {
		log.Printf("[agent/probe] store_session.error agent=%s phase=%s err=%v", name, phase, err)
	}
	populateProbeModels(ctx, sess, &status)
	populateProbeCommands(ctx, sess, &status)
	return status
}


func safeProbeConfiguredAgentWithPool(ctx context.Context, name string, def Definition, pool *Pool, probeSessions *probeSessionStore, phase probePhase) (status Status) {
	defer func() {
		if r := recover(); r != nil {
			status = unavailableStatus(name, true, fmt.Sprintf("probe panic: %v", r), time.Now().UTC())
			log.Printf("[agent/probe] probe.panic agent=%s phase=%s recovered=%v", name, phase, r)
		}
	}()
	return probeConfiguredAgentWithPool(ctx, name, def, pool, probeSessions, phase)
}


func safeProbeInstalledAgentWithPool(ctx context.Context, name string, def Definition, pool *Pool, probeSessions *probeSessionStore, status Status, phase probePhase) (out Status) {
	defer func() {
		if r := recover(); r != nil {
			out = status
			out.Available = false
			out.ProbeError = fmt.Sprintf("probe panic: %v", r)
			out.LastProbe = time.Now().UTC()
			log.Printf("[agent/probe] probe_installed.panic agent=%s phase=%s recovered=%v", name, phase, r)
		}
	}()
	return probeInstalledAgentWithPool(ctx, name, def, pool, probeSessions, status, phase)
}

