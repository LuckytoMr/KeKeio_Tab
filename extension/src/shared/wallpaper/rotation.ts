export type WallpaperPick = {
  id: string;
  nextHistory: string[];
};

export type WallpaperRotationCandidateInput = {
  primaryIds: string[];
  fallbackIds: string[];
  currentId: string;
};

function unique(ids: string[]) {
  return Array.from(new Set(ids.filter(Boolean)));
}

export function normalizeWallpaperIntervalSeconds(value: number) {
  const normalized = Number.isFinite(value) ? Math.floor(value) : 60;
  return Math.min(86400, Math.max(1, normalized || 1));
}

export function getWallpaperRotationDelayMs(intervalSeconds: number, lastRotationAt: number, now = Date.now()) {
  const intervalMs = normalizeWallpaperIntervalSeconds(intervalSeconds) * 1000;
  return Math.max(0, lastRotationAt + intervalMs - now);
}

export function buildWallpaperRotationCandidates(input: WallpaperRotationCandidateInput) {
  const primary = unique(input.primaryIds);
  const fallback = unique(input.fallbackIds);
  const pool = primary.length > 1 ? primary : fallback.length ? fallback : primary;
  const withoutCurrent = pool.filter((id) => id !== input.currentId);
  return withoutCurrent.length > 0 ? withoutCurrent : pool;
}

export function hasWallpaperRotationAlternative(candidateIds: string[], currentId: string) {
  return unique(candidateIds).some((id) => id !== currentId);
}

export function pickNextWallpaper(candidates: string[], history: string[]): WallpaperPick {
  if (candidates.length === 0) {
    throw new Error("NO_WALLPAPER_CANDIDATES");
  }

  const candidateSet = new Set(candidates);
  const validHistory = history.filter((id) => candidateSet.has(id));
  const used = new Set(validHistory);
  const available = candidates.filter((id) => !used.has(id));
  const pool = available.length > 0 ? available : candidates;
  const id = pool[Math.floor(Math.random() * pool.length)];
  const nextHistory = available.length > 0 ? [...validHistory, id] : [id];

  return { id, nextHistory };
}
