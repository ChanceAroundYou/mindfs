import { describe, expect, it } from "vitest";
import { resolveGroupColor } from "../src/shared/sessionGroupDisplay";

describe("session group display color resolution", () => {
  it("uses the node scoped color before registry fallbacks", () => {
    const byKey = {
      "pc::proj-a": { _nodeColor: "#c9b84a" },
      "home::proj-b": { _nodeColor: "#7c6bd6" },
    };
    const nodes = [
      { id: "local", name: "local", color: "#3b82f6" },
      { id: "pc", name: "PC", color: "#f59e0b" },
    ];

    expect(resolveGroupColor({ rootId: "proj-a", _nodeId: "pc" }, byKey, nodes)).toBe("#c9b84a");
    expect(resolveGroupColor({ rootId: "proj-b", _nodeId: "home" }, byKey, nodes)).toBe("#7c6bd6");
  });

  it("falls back through node id and name, then returns null", () => {
    const byKey = { "pc::proj-a": { _nodeColor: "#c9b84a" } };
    const nodes = [
      { id: "local", name: "local", color: "#3b82f6" },
      { id: "pc", name: "PC", color: "#f59e0b" },
    ];

    expect(resolveGroupColor({ rootId: "proj-x", _nodeId: "pc" }, byKey, nodes)).toBe("#f59e0b");
    expect(resolveGroupColor({ rootId: "proj-y", _nodeId: "pc", _nodeName: "PC" }, {}, nodes)).toBe("#f59e0b");
    expect(resolveGroupColor({ rootId: "proj-z", _nodeId: "ghost" }, {}, nodes)).toBeNull();
    expect(resolveGroupColor({ rootId: "", _nodeId: "" }, {}, nodes)).toBeNull();
  });
});
