import {
  isSameWritableSyncSession,
  type PublicWorkerSession
} from "./credentialVault";
import { ScopedRequestGate, type ScopedRequestToken } from "./requestGate";

function requestScope(session: PublicWorkerSession) {
  return JSON.stringify([
    session.baseUrl,
    session.accountScope,
    session.sessionGeneration
  ]);
}

export class WritableSessionRequestGate {
  private readonly gate = new ScopedRequestGate();

  begin(session: PublicWorkerSession) {
    return this.gate.begin(requestScope(session));
  }

  isCurrent(
    token: ScopedRequestToken,
    expected: PublicWorkerSession,
    current?: PublicWorkerSession | null
  ) {
    return token.scope === requestScope(expected)
      && this.gate.isCurrent(token)
      && expected.baseUrl === current?.baseUrl
      && isSameWritableSyncSession(expected, current);
  }

  invalidate() {
    this.gate.invalidate();
  }
}
