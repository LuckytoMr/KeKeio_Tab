import { describe, expect, it } from "vitest";
import { buildSearchUrl, defaultSearchEngines, getSearchEngineIcon, mergeSearchEngines } from "./engines";

describe("defaultSearchEngines", () => {
  it("puts Baidu first and Google second", () => {
    expect(defaultSearchEngines[0].id).toBe("baidu");
    expect(defaultSearchEngines[1].id).toBe("google");
  });

  it("contains a broad full-pro engine set", () => {
    expect(defaultSearchEngines.length).toBeGreaterThanOrEqual(18);
    expect(defaultSearchEngines.map((engine) => engine.id)).toContain("bilibili");
    expect(defaultSearchEngines.map((engine) => engine.id)).toContain("github");
  });

  it("keeps built-in order when merging old profiles", () => {
    const merged = mergeSearchEngines([{ id: "google", title: "Google Old", template: "https://g.test?q={query}" }]);
    expect(merged[0].id).toBe("baidu");
    expect(merged[1].id).toBe("google");
    expect(merged[1].template).toBe("https://g.test?q={query}");
  });

  it("builds an encoded search URL", () => {
    expect(buildSearchUrl(defaultSearchEngines[0], "测试 test")).toBe(
      "https://www.baidu.com/s?wd=%E6%B5%8B%E8%AF%95%20test"
    );
  });

  it("maps search engines to compact brand icons", () => {
    expect(getSearchEngineIcon(defaultSearchEngines[0])).toMatchObject({ kind: "baidu", label: "百度" });
    expect(getSearchEngineIcon(defaultSearchEngines[1])).toMatchObject({ kind: "google", label: "Google" });
    expect(getSearchEngineIcon({ id: "custom", title: "Quark" })).toMatchObject({
      kind: "initial",
      label: "Q"
    });
  });
});
