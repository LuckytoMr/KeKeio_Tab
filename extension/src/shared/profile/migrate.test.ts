import { describe, expect, it } from "vitest";
import { migrateProfile } from "./migrate";

describe("migrateProfile", () => {
  it("signs out profile metadata that points at a former configurable backend", () => {
    const previous = migrateProfile(undefined);
    previous.sync = {
      provider: "backend",
      status: "ready",
      backendUrl: "http://localhost:8787",
      lastSyncedAt: "2026-07-12T00:00:00.000Z"
    };

    expect(migrateProfile(previous).sync).toMatchObject({
      provider: "backend",
      status: "signed-out",
      backendUrl: "https://tab.kekeio.com",
      errorMessage: "云端服务地址已升级，请重新登录。"
    });
  });

  it("adds full-pro defaults to an old profile", () => {
    const migrated = migrateProfile({
      schemaVersion: 1,
      profileId: "profile:old",
      deviceId: "device:old",
      updatedAt: "2026-01-01T00:00:00.000Z",
      groups: [],
      shortcuts: [],
      search: {
        mode: "custom",
        disposition: "CURRENT_TAB",
        selectedEngineId: "google",
        engines: [{ id: "google", title: "Google", template: "https://google.test/search?q={query}" }]
      },
      wallpaper: {
        selected: { kind: "builtin", id: "mist" },
        selectedIds: ["mist"],
        activeSourceTab: "official",
        rotationMode: "manual",
        rotationHistory: [],
        activeCategory: "all",
        overlayOpacity: 0.58,
        blur: 0
      },
      theme: {
        styleId: "quark-flow",
        density: "comfortable",
        sidebarSide: "left",
        showBrand: true,
        columns: 6
      },
      sync: {
        provider: "none",
        status: "disabled"
      }
    } as any);

    expect(migrated.search.engines[0].id).toBe("baidu");
    expect(migrated.search.engines.length).toBeGreaterThanOrEqual(18);
    expect(migrated.groups.length).toBeGreaterThan(0);
    expect(migrated.shortcuts.length).toBeGreaterThan(0);
    expect(migrated.wallpaper.overlayOpacity).toBeLessThanOrEqual(0.2);
    expect(migrated.theme.columns).toBe(6);
    expect(migrated.theme.rows).toBe(2);
    expect(migrated.theme.iconSize).toBe("mini");
    expect(migrated.theme.iconShape).toBe("circle");
    expect(migrated.wallpaper.rotationIntervalSeconds).toBe(60);
    expect(migrated.wallpaper.rotationSource).toBe("selected");
    expect(migrated.wallpaper.selectedIds).toContain("mist");
  });

  it("moves the previous untouched visual defaults to the new Quark-like defaults", () => {
    const migrated = migrateProfile({
      ...migrateProfile(undefined),
      theme: {
        styleId: "quark-flow",
        density: "comfortable",
        sidebarSide: "left",
        showBrand: false,
        columns: 6,
        rows: 2,
        iconSize: "medium",
        iconShape: "squircle"
      }
    });

    expect(migrated.theme.columns).toBe(8);
    expect(migrated.theme.iconSize).toBe("mini");
    expect(migrated.theme.iconShape).toBe("circle");
  });

  it("moves the immediately previous untouched defaults to the corrected small default", () => {
    const migrated = migrateProfile({
      ...migrateProfile(undefined),
      theme: {
        styleId: "quark-flow",
        density: "comfortable",
        sidebarSide: "left",
        showBrand: false,
        columns: 8,
        rows: 2,
        iconSize: "tiny",
        iconShape: "circle"
      }
    });

    expect(migrated.theme.columns).toBe(8);
    expect(migrated.theme.iconSize).toBe("mini");
    expect(migrated.theme.iconShape).toBe("circle");
  });

  it("does not overwrite customized visual settings during migration", () => {
    const migrated = migrateProfile({
      ...migrateProfile(undefined),
      theme: {
        styleId: "quark-flow",
        density: "compact",
        sidebarSide: "left",
        showBrand: false,
        columns: 6,
        rows: 2,
        iconSize: "medium",
        iconShape: "squircle"
      }
    });

    expect(migrated.theme.columns).toBe(6);
    expect(migrated.theme.iconSize).toBe("medium");
    expect(migrated.theme.iconShape).toBe("squircle");
  });

  it.each(["large", "xlarge"] as const)("maps the removed %s size to the supported large size", (iconSize) => {
    const migrated = migrateProfile({
      ...migrateProfile(undefined),
      theme: {
        ...migrateProfile(undefined).theme,
        iconSize
      }
    } as any);

    expect(migrated.theme.iconSize).toBe("medium");
  });

  it("upgrades old generated text icons to automatic favicons", () => {
    const migrated = migrateProfile({
      schemaVersion: 1,
      profileId: "profile:old",
      deviceId: "device:old",
      updatedAt: "2026-01-01T00:00:00.000Z",
      groups: [{ id: "group:work", title: "工作台", sortIndex: 0, createdAt: "", updatedAt: "" }],
      shortcuts: [
        {
          id: "shortcut:old",
          groupId: "group:work",
          title: "Google",
          url: "https://www.google.com/",
          icon: { kind: "text", text: "G" },
          sortIndex: 0,
          createdAt: "",
          updatedAt: ""
        }
      ],
      search: {
        mode: "custom",
        disposition: "CURRENT_TAB",
        selectedEngineId: "baidu",
        engines: []
      },
      wallpaper: {
        selected: { kind: "builtin", id: "mist" },
        selectedIds: ["mist"],
        activeSourceTab: "official",
        rotationMode: "manual",
        rotationHistory: [],
        activeCategory: "all",
        overlayOpacity: 0.16,
        blur: 0
      },
      theme: {
        styleId: "quark-flow",
        density: "comfortable",
        sidebarSide: "left",
        showBrand: true,
        columns: 6
      },
      sync: {
        provider: "none",
        status: "disabled"
      }
    } as any);

    expect(migrated.shortcuts[0].icon).toMatchObject({
      kind: "favicon",
      url: "https://www.gstatic.com/images/branding/product/2x/googleg_48dp.png"
    });
  });

  it("upgrades known blocked favicon URLs to stable icon assets", () => {
    const migrated = migrateProfile({
      schemaVersion: 1,
      profileId: "profile:old",
      deviceId: "device:old",
      updatedAt: "2026-01-01T00:00:00.000Z",
      groups: [{ id: "group:work", title: "工作台", sortIndex: 0, createdAt: "", updatedAt: "" }],
      shortcuts: [
        {
          id: "shortcut:gmail",
          groupId: "group:work",
          title: "Gmail",
          url: "https://mail.google.com/",
          icon: { kind: "favicon", url: "https://mail.google.com/favicon.ico", fallbackText: "GM" },
          sortIndex: 0,
          createdAt: "",
          updatedAt: ""
        }
      ],
      search: {
        mode: "custom",
        disposition: "CURRENT_TAB",
        selectedEngineId: "baidu",
        engines: []
      },
      wallpaper: {
        selected: { kind: "builtin", id: "mist" },
        selectedIds: ["mist"],
        activeSourceTab: "official",
        rotationMode: "manual",
        rotationHistory: [],
        activeCategory: "all",
        overlayOpacity: 0.06,
        blur: 0
      },
      theme: {
        styleId: "quark-flow",
        density: "comfortable",
        sidebarSide: "left",
        showBrand: true,
        columns: 6,
        rows: 2
      },
      sync: {
        provider: "none",
        status: "disabled"
      }
    } as any);

    expect(migrated.shortcuts[0].icon).toMatchObject({
      kind: "favicon",
      url: "https://ssl.gstatic.com/ui/v1/icons/mail/rfr/gmail.ico"
    });
  });

  it("refreshes old generated local icon caches for known high resolution sources", () => {
    const migrated = migrateProfile({
      schemaVersion: 1,
      profileId: "profile:old",
      deviceId: "device:old",
      updatedAt: "2026-01-01T00:00:00.000Z",
      groups: [{ id: "group:work", title: "工作台", sortIndex: 0, createdAt: "", updatedAt: "" }],
      shortcuts: [
        {
          id: "shortcut:google",
          groupId: "group:work",
          title: "Google",
          url: "https://www.google.com/",
          icon: { kind: "local", assetId: "icon:low-res", localOnly: true },
          sortIndex: 0,
          createdAt: "",
          updatedAt: ""
        }
      ],
      search: {
        mode: "custom",
        disposition: "CURRENT_TAB",
        selectedEngineId: "baidu",
        engines: []
      },
      wallpaper: {
        selected: { kind: "builtin", id: "mist" },
        selectedIds: ["mist"],
        activeSourceTab: "official",
        rotationMode: "manual",
        rotationHistory: [],
        activeCategory: "all",
        overlayOpacity: 0.06,
        blur: 0
      },
      theme: {
        styleId: "quark-flow",
        density: "comfortable",
        sidebarSide: "left",
        showBrand: false,
        columns: 6,
        rows: 2
      },
      sync: {
        provider: "none",
        status: "disabled"
      }
    } as any);

    expect(migrated.shortcuts[0].icon).toMatchObject({
      kind: "favicon",
      url: "https://www.gstatic.com/images/branding/product/2x/googleg_48dp.png"
    });
  });
});
