import { describe, expect, it } from "vitest";
import {
  buildWallpaperRotationCandidates,
  hasWallpaperRotationAlternative,
  getWallpaperRotationDelayMs,
  normalizeWallpaperIntervalSeconds,
  pickNextWallpaper
} from "./rotation";

describe("pickNextWallpaper", () => {
  it("does not repeat a wallpaper until every candidate has been used", () => {
    const candidates = ["a", "b", "c"];
    const first = pickNextWallpaper(candidates, []);
    const second = pickNextWallpaper(candidates, [first.id]);
    const third = pickNextWallpaper(candidates, [first.id, second.id]);

    expect(new Set([first.id, second.id, third.id]).size).toBe(3);
  });

  it("starts a new cycle when history contains every candidate", () => {
    const next = pickNextWallpaper(["a", "b"], ["a", "b"]);
    expect(["a", "b"]).toContain(next.id);
    expect(next.nextHistory).toEqual([next.id]);
  });

  it("uses fallback wallpapers when the selected pool cannot change", () => {
    expect(
      buildWallpaperRotationCandidates({
        primaryIds: ["mist"],
        fallbackIds: ["mist", "ink", "aurora"],
        currentId: "mist"
      })
    ).toEqual(["ink", "aurora"]);
  });

  it("allows automatic rotation every second", () => {
    expect(normalizeWallpaperIntervalSeconds(0)).toBe(1);
    expect(normalizeWallpaperIntervalSeconds(1)).toBe(1);
    expect(normalizeWallpaperIntervalSeconds(60)).toBe(60);
  });

  it("detects when a rotation pool has no alternative to the current wallpaper", () => {
    expect(hasWallpaperRotationAlternative(["web:only:4k"], "web:only:4k")).toBe(false);
    expect(hasWallpaperRotationAlternative(["web:only:4k"], "mist")).toBe(true);
    expect(hasWallpaperRotationAlternative(["web:only:4k", "web:next:4k"], "web:only:4k")).toBe(true);
  });

  it("keeps an absolute rotation deadline when rerenders happen mid-interval", () => {
    expect(getWallpaperRotationDelayMs(60, 1_000, 31_000)).toBe(30_000);
    expect(getWallpaperRotationDelayMs(60, 1_000, 61_500)).toBe(0);
  });
});
