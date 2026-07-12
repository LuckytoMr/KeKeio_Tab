import { describe, expect, it } from "vitest";
import type { PublicWorkerSession } from "./credentialVault";
import { WritableSessionRequestGate } from "./writableSessionRequestGate";

function session(overrides: Partial<PublicWorkerSession> = {}): PublicWorkerSession {
  return {
    accountScope: "account:a",
    sessionGeneration: "session:a",
    baseUrl: "https://sync.example.test",
    userId: "user:a",
    email: "a@example.test",
    scope: "full",
    readOnly: false,
    firstConnectionPending: false,
    accessExpiresAt: 999_999,
    refreshExpiresAt: 9_999_999,
    updatedAt: 1,
    ...overrides
  };
}

describe("WritableSessionRequestGate", () => {
  it("rejects a delayed response after logout invalidates its request", () => {
    const gate = new WritableSessionRequestGate();
    const auth = session();
    const request = gate.begin(auth);

    gate.invalidate();

    expect(gate.isCurrent(request, auth, null)).toBe(false);
  });

  it("rejects an old response after the active account and generation are replaced", () => {
    const gate = new WritableSessionRequestGate();
    const first = session();
    const firstRequest = gate.begin(first);
    const replacement = session({
      accountScope: "account:b",
      sessionGeneration: "session:b",
      userId: "user:b",
      email: "b@example.test"
    });
    const replacementRequest = gate.begin(replacement);

    expect(gate.isCurrent(firstRequest, first, replacement)).toBe(false);
    expect(gate.isCurrent(replacementRequest, replacement, replacement)).toBe(true);
  });
});
