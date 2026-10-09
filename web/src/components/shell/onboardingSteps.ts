import type { MessageKey } from "../../i18n/index";

export type TourStep = {
  id: string;
  target: string;
  fallbackTarget?: string;
  titleKey: MessageKey;
  bodyKey: MessageKey;
  interactive?: boolean;
  padding?: number;
  pinToTop?: boolean;
};

export const TOUR_STEPS: TourStep[] = [
  { id: "sidebar-menu", target: "[data-onboarding='sidebar-menu']", titleKey: "onboarding.sidebarMenu.title", bodyKey: "onboarding.sidebarMenu.body", pinToTop: true },
  { id: "project-tabs", target: "[data-onboarding='project-tabs']", titleKey: "onboarding.projectTabs.title", bodyKey: "onboarding.projectTabs.body", pinToTop: true },
  { id: "project-home", target: "[data-onboarding='project-home']", titleKey: "onboarding.projectHome.title", bodyKey: "onboarding.projectHome.body", pinToTop: true },
  { id: "main-menu", target: "[data-onboarding='main-menu']", titleKey: "onboarding.mainMenu.title", bodyKey: "onboarding.mainMenu.body", pinToTop: true },
  { id: "task-templates", target: "[data-onboarding='task-templates']", titleKey: "onboarding.taskTemplates.title", bodyKey: "onboarding.taskTemplates.body" },
  { id: "task-template-menu", target: "[data-onboarding='task-template-menu']", titleKey: "onboarding.taskTemplateMenu.title", bodyKey: "onboarding.taskTemplateMenu.body" },
  { id: "tasks", target: "[data-onboarding='task-create']", titleKey: "onboarding.tasks.title", bodyKey: "onboarding.tasks.body" },
  { id: "session-actions", target: "[data-onboarding='session-actions']", titleKey: "onboarding.sessionActions.title", bodyKey: "onboarding.sessionActions.body", padding: 1, pinToTop: true },
  { id: "shortcuts", target: "[data-onboarding='message-input']", titleKey: "onboarding.shortcuts.title", bodyKey: "onboarding.shortcuts.body" },
  { id: "mode-selector", target: "[data-onboarding='mode-selector']", titleKey: "onboarding.modeSelector.title", bodyKey: "onboarding.modeSelector.body", interactive: true },
  { id: "agent-selector", target: "[data-onboarding='agent-selector']", fallbackTarget: "[data-onboarding='input-controls']", titleKey: "onboarding.agentSelector.title", bodyKey: "onboarding.agentSelector.body", interactive: true },
  { id: "attachment-action", target: "[data-onboarding='attachment-action']", titleKey: "onboarding.attachmentAction.title", bodyKey: "onboarding.attachmentAction.body" },
  { id: "send-action", target: "[data-onboarding='send-action']", titleKey: "onboarding.sendAction.title", bodyKey: "onboarding.sendAction.body" },
];
