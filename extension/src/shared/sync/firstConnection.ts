export type FirstConnectionKind = "both-empty" | "local-only" | "remote-only" | "both-have-data";
export type FirstConnectionStrategy = "use-local" | "use-remote";

export function getAutomaticFirstConnectionStrategy(kind: FirstConnectionKind): FirstConnectionStrategy | null {
  if (kind === "remote-only") return "use-remote";
  if (kind === "both-have-data") return null;
  return "use-local";
}

export function requiresExplicitReadOnlyRemoteApproval(kind: FirstConnectionKind) {
  return kind === "both-have-data";
}

export async function resolveFirstConnectionStrategy(
  kind: FirstConnectionKind,
  requestDecision: () => Promise<FirstConnectionStrategy | null>,
  cancelPendingConnection: () => Promise<void>
): Promise<FirstConnectionStrategy | null> {
  const automatic = getAutomaticFirstConnectionStrategy(kind);
  if (automatic) return automatic;

  const decision = await requestDecision();
  if (decision) return decision;

  await cancelPendingConnection();
  return null;
}
