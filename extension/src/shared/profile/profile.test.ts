import { describe, expect, it } from "vitest";
import { createDefaultProfile } from "./defaults";
import {
  addShortcutGroup,
  deleteShortcut,
  deleteShortcutGroup,
  moveShortcut,
  renameShortcutGroup,
  swapShortcutGroupOrder,
  swapShortcutOrder,
  upsertShortcut
} from "./mutations";

describe("profile mutations", () => {
  it("uses a low default wallpaper overlay so images stay close to the original", () => {
    const profile = createDefaultProfile();

    expect(profile.wallpaper.overlayOpacity).toBeLessThanOrEqual(0.2);
  });

  it("uses a one-minute automatic wallpaper interval by default", () => {
    const profile = createDefaultProfile();

    expect(profile.wallpaper.rotationIntervalSeconds).toBe(60);
    expect(profile.wallpaper.rotationSource).toBe("selected");
  });

  it("uses the Quark-like compact visual defaults", () => {
    const profile = createDefaultProfile();

    expect(profile.theme.columns).toBe(8);
    expect(profile.theme.iconSize).toBe("tiny");
    expect(profile.theme.iconShape).toBe("circle");
  });

  it("uses the exported profile defaults for groups and shortcut order", () => {
    const profile = createDefaultProfile();

    expect(profile.groups).toMatchObject([{ id: "group:media", title: "常用", sortIndex: 0 }]);
    expect(profile.shortcuts.map((shortcut) => [shortcut.id, shortcut.groupId, shortcut.sortIndex])).toEqual([
      ["shortcut:youtube", "group:media", 0],
      ["shortcut:github", "group:media", 1],
      ["shortcut:cloudflare", "group:media", 2],
      ["shortcut:google", "group:media", 3],
      ["shortcut:gmail", "group:media", 4]
    ]);
  });

  it("uses high resolution automatic icons for known default shortcuts", () => {
    const profile = createDefaultProfile();

    expect(profile.shortcuts.find((shortcut) => shortcut.id === "shortcut:google")?.icon).toMatchObject({
      kind: "favicon",
      url: "https://www.gstatic.com/images/branding/product/2x/googleg_48dp.png"
    });
  });

  it("adds a shortcut to the requested group", () => {
    const profile = createDefaultProfile();
    const groupId = profile.groups[0].id;
    const updated = upsertShortcut(profile, {
      id: "shortcut:test",
      groupId,
      title: "Test",
      url: "example.com",
      icon: { kind: "favicon", url: "https://example.com/favicon.ico", fallbackText: "TE" }
    });

    expect(updated.shortcuts.some((shortcut) => shortcut.id === "shortcut:test")).toBe(true);
    expect(updated.shortcuts.find((shortcut) => shortcut.id === "shortcut:test")?.url).toBe("https://example.com/");
  });

  it("marks a shortcut deleted instead of removing it immediately", () => {
    const profile = createDefaultProfile();
    const shortcut = profile.shortcuts[0];
    const updated = deleteShortcut(profile, shortcut.id, "device:test");

    const deleted = updated.shortcuts.find((item) => item.id === shortcut.id);
    expect(deleted?.deletedAt).toBeTruthy();
    expect(deleted?.deletedByDeviceId).toBe("device:test");
  });

  it("swaps shortcut order inside the same group", () => {
    const profile = createDefaultProfile();
    const first = profile.shortcuts.find((shortcut) => shortcut.id === "shortcut:google")!;
    const second = profile.shortcuts.find((shortcut) => shortcut.id === "shortcut:gmail")!;
    const updated = swapShortcutOrder(profile, first.id, second.id);

    expect(updated.shortcuts.find((shortcut) => shortcut.id === first.id)?.sortIndex).toBe(second.sortIndex);
    expect(updated.shortcuts.find((shortcut) => shortcut.id === second.id)?.sortIndex).toBe(first.sortIndex);
  });

  it("inserts a shortcut before the hovered shortcut inside the same group", () => {
    const profile = createDefaultProfile();
    const updated = moveShortcut(profile, "shortcut:gmail", "group:media", "shortcut:github");
    const orderedIds = updated.shortcuts
      .filter((shortcut) => shortcut.groupId === "group:media" && !shortcut.deletedAt)
      .sort((left, right) => left.sortIndex - right.sortIndex)
      .map((shortcut) => shortcut.id);

    expect(orderedIds).toEqual([
      "shortcut:youtube",
      "shortcut:gmail",
      "shortcut:github",
      "shortcut:cloudflare",
      "shortcut:google"
    ]);
    expect(
      updated.shortcuts
        .filter((shortcut) => shortcut.groupId === "group:media" && !shortcut.deletedAt)
        .map((shortcut) => shortcut.sortIndex)
        .sort((left, right) => left - right)
    ).toEqual([0, 1, 2, 3, 4]);
  });

  it("moves a shortcut forward to the hovered shortcut position", () => {
    const profile = createDefaultProfile();
    const updated = moveShortcut(profile, "shortcut:github", "group:media", "shortcut:cloudflare");
    const orderedIds = updated.shortcuts
      .filter((shortcut) => shortcut.groupId === "group:media" && !shortcut.deletedAt)
      .sort((left, right) => left.sortIndex - right.sortIndex)
      .map((shortcut) => shortcut.id);

    expect(orderedIds).toEqual([
      "shortcut:youtube",
      "shortcut:cloudflare",
      "shortcut:github",
      "shortcut:google",
      "shortcut:gmail"
    ]);
  });

  it("appends a shortcut to the end of its current group when no target is supplied", () => {
    const profile = createDefaultProfile();
    const updated = moveShortcut(profile, "shortcut:youtube", "group:media");
    const orderedIds = updated.shortcuts
      .filter((shortcut) => shortcut.groupId === "group:media" && !shortcut.deletedAt)
      .sort((left, right) => left.sortIndex - right.sortIndex)
      .map((shortcut) => shortcut.id);

    expect(orderedIds).toEqual([
      "shortcut:github",
      "shortcut:cloudflare",
      "shortcut:google",
      "shortcut:gmail",
      "shortcut:youtube"
    ]);
  });

  it("moves a shortcut into another group before the selected target", () => {
    const withGroup = addShortcutGroup(createDefaultProfile(), "工具");
    const tools = withGroup.groups.find((group) => group.title === "工具")!;
    const withTarget = upsertShortcut(withGroup, {
      id: "shortcut:tool",
      groupId: tools.id,
      title: "Tool",
      url: "https://example.com",
      icon: { kind: "text", text: "TO" }
    });
    const updated = moveShortcut(withTarget, "shortcut:github", tools.id, "shortcut:tool");
    const toolOrder = updated.shortcuts
      .filter((shortcut) => shortcut.groupId === tools.id && !shortcut.deletedAt)
      .sort((left, right) => left.sortIndex - right.sortIndex)
      .map((shortcut) => shortcut.id);

    expect(toolOrder).toEqual(["shortcut:github", "shortcut:tool"]);
    expect(
      updated.shortcuts
        .filter((shortcut) => shortcut.groupId === "group:media" && !shortcut.deletedAt)
        .sort((left, right) => left.sortIndex - right.sortIndex)
        .map((shortcut) => shortcut.sortIndex)
    ).toEqual([0, 1, 2, 3]);
  });

  it("appends a shortcut when it is moved into an empty group", () => {
    const withGroup = addShortcutGroup(createDefaultProfile(), "空分组");
    const emptyGroup = withGroup.groups.find((group) => group.title === "空分组")!;
    const updated = moveShortcut(withGroup, "shortcut:youtube", emptyGroup.id);
    const moved = updated.shortcuts.find((shortcut) => shortcut.id === "shortcut:youtube");

    expect(moved).toMatchObject({ groupId: emptyGroup.id, sortIndex: 0 });
  });

  it("appends an edited shortcut when its group changes in the form", () => {
    const withGroup = addShortcutGroup(createDefaultProfile(), "工具");
    const tools = withGroup.groups.find((group) => group.title === "工具")!;
    const withExistingTarget = upsertShortcut(withGroup, {
      id: "shortcut:tool",
      groupId: tools.id,
      title: "Tool",
      url: "https://example.com",
      icon: { kind: "text", text: "TO" }
    });
    const github = withExistingTarget.shortcuts.find((shortcut) => shortcut.id === "shortcut:github")!;
    const updated = upsertShortcut(withExistingTarget, {
      id: github.id,
      groupId: tools.id,
      title: github.title,
      url: github.url,
      icon: github.icon
    });

    expect(updated.shortcuts.find((shortcut) => shortcut.id === github.id)).toMatchObject({
      groupId: tools.id,
      sortIndex: 1
    });
    expect(
      updated.shortcuts
        .filter((shortcut) => shortcut.groupId === "group:media" && !shortcut.deletedAt)
        .map((shortcut) => shortcut.sortIndex)
        .sort((left, right) => left - right)
    ).toEqual([0, 1, 2, 3]);
  });

  it("adds, renames, reorders, and deletes shortcut groups without losing shortcuts", () => {
    const profile = createDefaultProfile();
    const added = addShortcutGroup(profile, "工具");
    const tools = added.groups.find((group) => group.title === "工具")!;
    const renamed = renameShortcutGroup(added, tools.id, "内网");
    const withToolShortcut = upsertShortcut(renamed, {
      id: "shortcut:tool",
      groupId: tools.id,
      title: "Tool",
      url: "https://example.com",
      icon: { kind: "favicon", url: "https://example.com/favicon.ico", fallbackText: "TO" }
    });
    const swapped = swapShortcutGroupOrder(withToolShortcut, tools.id, "group:media");
    const deleted = deleteShortcutGroup(swapped, tools.id);

    expect(renamed.groups.find((group) => group.id === tools.id)?.title).toBe("内网");
    expect(swapped.groups.find((group) => group.id === tools.id)?.sortIndex).toBe(
      withToolShortcut.groups.find((group) => group.id === "group:media")?.sortIndex
    );
    expect(deleted.groups.find((group) => group.id === tools.id)?.deletedAt).toBeTruthy();
    expect(deleted.groups.filter((group) => !group.deletedAt).some((group) => group.id === tools.id)).toBe(false);
    expect(deleted.shortcuts.filter((shortcut) => shortcut.title === "Tool")[0].groupId).toBe("group:media");
  });
});
