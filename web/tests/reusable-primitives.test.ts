import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { filterAgentModels } from "../src/shared/modelFiltering";
import type { AgentModelInfo } from "../src/shared/agentTypes";
import { ExitIcon } from "../src/components/common/ExitIcon";
import { NoWorktreeIcon } from "../src/components/common/NoWorktreeIcon";

const models: AgentModelInfo[] = [
  { id: "opus-4", name: "Claude Opus", description: "reasoning model" },
  { id: "gpt-5", name: "GPT Five", description: "general assistant" },
  { id: "sonnet", name: "Sonnet", description: "Fast CODE model" },
];

describe("reusable frontend primitives", () => {
  it("filters models by id, name, or description without case sensitivity", () => {
    expect(filterAgentModels(models, " OPUS ").map(({ id }) => id)).toEqual(["opus-4"]);
    expect(filterAgentModels(models, "gpt five").map(({ id }) => id)).toEqual(["gpt-5"]);
    expect(filterAgentModels(models, "code").map(({ id }) => id)).toEqual(["sonnet"]);
    expect(filterAgentModels(models, "missing")).toEqual([]);
  });

  it("keeps the shared filter independent of services and views", () => {
    const source = readFileSync(new URL("../src/shared/modelFiltering.ts", import.meta.url), "utf8");
    expect(source).not.toMatch(/from ["'][^"']*(?:services|components|react)\//);
  });

  it("returns the original list for an empty or whitespace query", () => {
    expect(filterAgentModels(models, "")).toBe(models);
    expect(filterAgentModels(models, "   ")).toBe(models);
  });

  it("keeps the exit icon decorative and at its standard size", () => {
    const icon = ExitIcon();
    expect(icon.type).toBe("svg");
    expect(icon.props).toMatchObject({ width: "18", height: "18", "aria-hidden": "true" });
  });

  it("keeps the no-worktree marker decorative and honors its requested size", () => {
    const defaultIcon = NoWorktreeIcon({});
    const sizedIcon = NoWorktreeIcon({ size: 12 });
    expect(defaultIcon.props).toMatchObject({ width: 8, height: 8, "aria-hidden": "true" });
    expect(sizedIcon.props).toMatchObject({ width: 12, height: 12, "aria-hidden": "true" });
  });
});
