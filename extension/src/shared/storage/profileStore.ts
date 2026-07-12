import { createDefaultProfile } from "../profile/defaults";
import { migrateProfile } from "../profile/migrate";
import type { Profile } from "../profile/types";
import { SyncStore } from "./syncStore";

export type LegacyProfileLoader = () => Promise<Profile | undefined>;

export type ProfileInvalidation = {
  type: "profile-invalidated";
  profileId: string;
  revision: number;
  sourceId: string;
};

export interface ProfileInvalidationBus {
  publish(message: ProfileInvalidation): void;
  subscribe(listener: (message: ProfileInvalidation) => void): () => void;
}

export class ProfileStore {
  private knownRevision: number | undefined;
  private readonly sourceId = crypto.randomUUID();

  constructor(
    readonly syncStore: SyncStore,
    private readonly loadLegacy: LegacyProfileLoader,
    private readonly invalidationBus?: ProfileInvalidationBus
  ) {}

  subscribeInvalidation(listener: (message: ProfileInvalidation) => void) {
    if (!this.invalidationBus) return () => undefined;
    return this.invalidationBus.subscribe((message) => {
      if (message.sourceId !== this.sourceId) listener(message);
    });
  }

  private publish(profileId: string, revision: number) {
    this.invalidationBus?.publish({
      type: "profile-invalidated",
      profileId,
      revision,
      sourceId: this.sourceId
    });
  }

  async load() {
    const current = await this.syncStore.getLocalProfile();
    if (current) {
      this.knownRevision = current.revision;
      return migrateProfile(current.profile);
    }
    const legacy = await this.loadLegacy();
    const migrated = migrateProfile(legacy ?? createDefaultProfile());
    await this.syncStore.initialize(migrated, { hasUserEdits: Boolean(legacy) });
    this.knownRevision = (await this.syncStore.getLocalProfile())?.revision ?? 0;
    return migrated;
  }

  async save(profile: Profile) {
    const migrated = migrateProfile(profile);
    await this.syncStore.initialize(migrated);
    const current = await this.syncStore.getLocalProfile();
    if (this.knownRevision === undefined) this.knownRevision = current?.revision ?? 0;
    const committed = await this.syncStore.commitProfile(migrated, Date.now(), this.knownRevision);
    this.knownRevision = committed.revision;
    this.publish(migrated.profileId, committed.revision);
    return migrated;
  }

  async reset() {
    const current = await this.load();
    const defaults = createDefaultProfile();
    const reset: Profile = {
      ...defaults,
      profileId: current.profileId,
      deviceId: current.deviceId,
      updatedAt: new Date().toISOString(),
      sync: current.sync
    };
    const committed = await this.syncStore.commitProfile(reset, Date.now(), this.knownRevision);
    this.knownRevision = committed.revision;
    this.publish(reset.profileId, committed.revision);
    return reset;
  }
}

export const syncStore = new SyncStore();
