import Dexie, { type Table } from "dexie";

export type LocalAsset = {
  assetId: string;
  type: "wallpaper" | "icon";
  name: string;
  mimeType: string;
  size: number;
  blob: Blob;
  createdAt: string;
  lastUsedAt: string;
};

class FullProDatabase extends Dexie {
  assets!: Table<LocalAsset, string>;

  constructor() {
    super("FullProNewTab");
    this.version(1).stores({
      assets: "&assetId,type,createdAt,lastUsedAt,size"
    });
  }
}

export const db = new FullProDatabase();
