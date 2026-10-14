import React from 'react';
import { useCachedAgentIcon } from './useCachedAgentIcon';

type AgentIconProps = {
  agentName: string;
  [key: string]: any; // Allow other props like style, etc.
};

const ICON_URLS: Record<string, { src: string; alt: string }> = {
  augment: { src: 'augment.svg', alt: 'Augment' },
  codex: { src: 'codex.svg', alt: 'Codex' },
  claude: { src: 'claude.svg', alt: 'Claude' },
  cline: { src: 'cline.svg', alt: 'Cline' },
  codebuddy: { src: 'codebuddy.svg', alt: 'CodeBuddy' },
  copilot: { src: 'copilot.svg', alt: 'Copilot' },
  cursor: { src: 'cursor.svg', alt: 'Cursor' },
  dsh: { src: 'dsh.svg', alt: 'DeepSeek Harness' },
  gemini: { src: 'gemini.svg', alt: 'Gemini' },
  grok: { src: 'grok.svg', alt: 'Grok' },
  hermes: { src: 'hermes.webp', alt: 'Hermes' },
  kiro: { src: 'kiro.svg', alt: 'Kiro' },
  kimi: { src: 'kimi.svg', alt: 'Kimi' },
  openclaw: { src: 'openclaw.svg', alt: 'OpenClaw' },
  opencode: { src: 'opencode.svg', alt: 'OpenCode' },
  omp: { src: 'omp.svg', alt: 'OMP' },
  pi: { src: 'pi.svg', alt: 'Pi' },
  qoder: { src: 'qoder.svg', alt: 'Qoder' },
  qwen: { src: 'qwen.svg', alt: 'Qwen' },
  reasonix: { src: 'reasonix.svg', alt: 'Reasonix' },
};

function fallbackIconText(agentName: string): string {
  const trimmed = agentName.trim();
  if (!trimmed) return 'AI';
  return trimmed.slice(0, 2).toUpperCase();
}

function cssSize(value: unknown, fallback: number): string {
  if (typeof value === 'number' && Number.isFinite(value)) return `${value}px`;
  if (typeof value === 'string' && value.trim()) return value;
  return `${fallback}px`;
}

export function AgentIcon({ agentName, ...props }: AgentIconProps) {
  const lowerAgentName = agentName.toLowerCase();
  const agentTokens = lowerAgentName.split(/[^a-z0-9]+/).filter(Boolean);
  const style = props.style ?? {};
  const width = style.width ?? props.width ?? 16;
  const height = style.height ?? props.height ?? 16;
  let icon: { src: string; alt: string } | null = null;
  if (lowerAgentName.includes('augment')) {
    icon = ICON_URLS.augment;
  } else if (lowerAgentName.includes('cursor')) {
    icon = ICON_URLS.cursor;
  } else if (lowerAgentName.includes('openclaw')) {
    icon = ICON_URLS.openclaw;
  } else if (lowerAgentName.includes('opencode')) {
    icon = ICON_URLS.opencode;
  } else if (lowerAgentName === 'omp' || lowerAgentName.includes('oh-my-pi')) {
    icon = ICON_URLS.omp;
  } else if (lowerAgentName.includes('copilot')) {
    icon = ICON_URLS.copilot;
  } else if (agentTokens.includes('pi') || lowerAgentName === 'pi') {
    icon = ICON_URLS.pi;
  } else if (lowerAgentName.includes('qoder')) {
    icon = ICON_URLS.qoder;
  } else if (lowerAgentName.includes('qwen')) {
    icon = ICON_URLS.qwen;
  } else if (lowerAgentName.includes('reasonix')) {
    icon = ICON_URLS.reasonix;
  } else if (agentTokens.includes('dsh')) {
    icon = ICON_URLS.dsh;
  } else if (lowerAgentName.includes('kiro')) {
    icon = ICON_URLS.kiro;
  } else if (lowerAgentName.includes('kimi')) {
    icon = ICON_URLS.kimi;
  } else if (lowerAgentName.includes('cline')) {
    icon = ICON_URLS.cline;
  } else if (lowerAgentName.includes('codebuddy')) {
    icon = ICON_URLS.codebuddy;
  } else if (lowerAgentName.includes('codex')) {
    icon = ICON_URLS.codex;
  } else if (lowerAgentName.includes('claude')) {
    icon = ICON_URLS.claude;
  } else if (lowerAgentName.includes('gemini')) {
    icon = ICON_URLS.gemini;
  } else if (lowerAgentName.includes('grok')) {
    icon = ICON_URLS.grok;
  } else if (lowerAgentName.includes('hermes')) {
    icon = ICON_URLS.hermes;
  }
  const iconSrc = useCachedAgentIcon(icon?.src);

  if (icon && iconSrc) {
    return (
      <img
        src={iconSrc}
        alt={icon.alt}
        width={width}
        height={height}
        {...props}
      />
    );
  }

  if (icon) {
    return (
      <span
        style={{
          display: 'inline-block',
          width: Number(width) || 16,
          height: Number(height) || 16,
        }}
        {...props}
      />
    );
  }

  const fallbackWidth = cssSize(width, 16);
  const fallbackHeight = cssSize(height, 16);
  return (
    <span
      {...props}
      aria-label={agentName}
      style={{
        ...style,
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: fallbackWidth,
        height: fallbackHeight,
        minWidth: fallbackWidth,
        borderRadius: '4px',
        background: 'rgba(148, 163, 184, 0.18)',
        color: 'var(--muted, #64748b)',
        fontSize: '9px',
        fontWeight: 700,
        lineHeight: 1,
        letterSpacing: 0,
      }}
    >
      {fallbackIconText(agentName)}
    </span>
  );
}
