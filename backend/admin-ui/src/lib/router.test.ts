import { describe, expect, it } from "vitest";
import {
  buildDetailHref,
  buildListHref,
  getReturnHref,
  parseAdminLocation,
  readListState,
  saveScrollPosition,
  takeScrollPosition
} from "./router";

describe("admin router", () => {
  it("maps every canonical route to a stable page", () => {
    expect(parseAdminLocation("/install").page).toBe("install");
    expect(parseAdminLocation("/admin/login").page).toBe("login");
    expect(parseAdminLocation("/admin").page).toBe("overview");
    expect(parseAdminLocation("/admin/users/user-42")).toMatchObject({ page: "user-detail", id: "user-42" });
    expect(parseAdminLocation("/admin/sync/attempts").page).toBe("sync-attempts");
    expect(parseAdminLocation("/admin/sync/conflicts").page).toBe("sync-conflicts");
    expect(parseAdminLocation("/admin/content/official/item%3A1")).toMatchObject({
      page: "content-detail",
      contentType: "official",
      id: "item:1"
    });
    expect(parseAdminLocation("/admin/releases").page).toBe("releases");
    expect(parseAdminLocation("/admin/audit/access").page).toBe("access-audit");
    expect(parseAdminLocation("/admin/security").page).toBe("security");
    expect(parseAdminLocation("/admin/maintenance").page).toBe("maintenance");
    expect(parseAdminLocation("/admin/backups").page).toBe("backups");
    expect(parseAdminLocation("/admin/system").page).toBe("system");
  });

  it("round-trips list filters, sorting and cursor through the URL", () => {
    const href = buildListHref("/admin/users", {
      query: "alice@example.com",
      status: "active",
      sort: "-lastActivityAt",
      cursor: "next:42"
    });
    expect(href).toBe("/admin/users?q=alice%40example.com&status=active&sort=-lastActivityAt&cursor=next%3A42");
    expect(readListState(new URL(href, "https://admin.local").searchParams)).toEqual({
      query: "alice@example.com",
      status: "active",
      sort: "-lastActivityAt",
      cursor: "next:42"
    });
  });

  it("keeps an explicit return URL when opening narrow-screen details", () => {
    const list = "/admin/content/styles?q=quiet&status=draft&sort=-updatedAt";
    const detail = buildDetailHref("styles", "style:quiet", list);
    expect(detail).toContain("/admin/content/styles/style%3Aquiet?");
    expect(getReturnHref(new URL(detail, "https://admin.local").searchParams, "/admin/content/styles")).toBe(list);
  });

  it("restores and consumes the saved list scroll position", () => {
    saveScrollPosition("/admin/users?q=alice", 684);
    expect(takeScrollPosition("/admin/users?q=alice")).toBe(684);
    expect(takeScrollPosition("/admin/users?q=alice")).toBeNull();
  });
});
