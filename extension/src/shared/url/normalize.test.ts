import { describe, expect, it } from "vitest";
import { normalizeShortcutUrl } from "./normalize";

describe("normalizeShortcutUrl", () => {
  it("adds https to bare domains", () => {
    expect(normalizeShortcutUrl("example.com/path")).toBe("https://example.com/path");
  });

  it("keeps supported protocols", () => {
    expect(normalizeShortcutUrl("http://localhost:3000")).toBe("http://localhost:3000/");
    expect(normalizeShortcutUrl("chrome://extensions")).toBe("chrome://extensions");
    expect(normalizeShortcutUrl("edge://extensions")).toBe("edge://extensions");
  });

  it("rejects scriptable protocols", () => {
    expect(() => normalizeShortcutUrl("javascript:alert(1)")).toThrow("BLOCKED_PROTOCOL");
    expect(() => normalizeShortcutUrl("data:text/html,hello")).toThrow("BLOCKED_PROTOCOL");
  });
});
