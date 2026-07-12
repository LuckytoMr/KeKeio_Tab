import { describe, expect, it } from "vitest";
import { ScopedRequestGate } from "./requestGate";

describe("ScopedRequestGate", () => {
  it("accepts only the latest request in the current scope", () => {
    const gate = new ScopedRequestGate();
    const first = gate.begin("https://one.example.test");
    const second = gate.begin("https://one.example.test");

    expect(gate.isCurrent(first)).toBe(false);
    expect(gate.isCurrent(second)).toBe(true);
  });

  it("invalidates old requests when the backend origin or session scope changes", () => {
    const gate = new ScopedRequestGate();
    const originOne = gate.begin("https://one.example.test");
    const originTwo = gate.begin("https://two.example.test");

    expect(gate.isCurrent(originOne)).toBe(false);
    expect(gate.isCurrent(originTwo)).toBe(true);
    gate.invalidate();
    expect(gate.isCurrent(originTwo)).toBe(false);
  });
});
