package app

import (
	"log"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/preferences"
)

func initializeCodexProfileSelection(prefs *preferences.Store) error {
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		return err
	}
	selections := prefs.AgentLastConfigSelections()
	selection := selections["codex"]
	name := ""
	switch {
	case selection.Type == "api_provider":
		if selection.ID == "api-llmux" {
			name = "llmux"
		} else {
			name = "default"
		}
	case selection.Type == "backup" && selection.ID == "shared:codex-llmux":
		name = "llmux"
	case selection.Type == "backup" && selection.ID == "shared:codex-default":
		name = "default"
	}

	var active string
	if name == "" {
		active, err = codexsync.InitializeActiveProfileFromRuntimeAt(paths)
	} else {
		active, err = codexsync.InitializeActiveProfileAt(paths, name)
	}
	if err != nil {
		return err
	}
	// 启动时按活动 profile 重渲染一次运行时配置。worker 没有周期性 sync
	// runner，渲染器升级或共享 profile 变化后，设备上的旧配置会一直留着
	// （pc-win 曾因此把本地根级键留在 [features] 里，Codex 直接打不开）。
	// 渲染是幂等的：内容一致时不写文件；失败只记日志，不阻断启动。
	if err := codexsync.RenderRuntimeAt(paths); err != nil {
		log.Printf("[codexsync] runtime.render.error err=%v", err)
	}
	profileID := "shared:codex-default"
	profileName := "default"
	if active == "llmux" {
		profileID = "shared:codex-llmux"
		profileName = "llmux"
	}
	return prefs.UpdateAgentLastConfigSelection("codex", preferences.LastConfigSelection{
		Type: "backup",
		ID:   profileID,
		Name: profileName,
	})
}
