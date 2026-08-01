import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { useState } from "preact/hooks";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog, FormErrorSummary } from "./common";

describe("accessible feedback components", () => {
  it("focuses a form error summary and links errors to their fields", async () => {
    render(
      <>
        <FormErrorSummary errors={{ email: "请输入管理员邮箱", password: "密码至少 4 位" }} focusOnMount />
        <input id="email" />
        <input id="password" />
      </>
    );

    const summary = await screen.findByRole("alert", { name: "请修正以下问题" });
    expect(summary).toHaveFocus();
    expect(screen.getByRole("link", { name: "请输入管理员邮箱" })).toHaveAttribute("href", "#email");
  });

  it("puts safe focus in a destructive dialog, traps focus and supports Escape", async () => {
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    const user = userEvent.setup();
    render(
      <ConfirmDialog
        open
        title="归档风格"
        description="归档后不会再提供给扩展。"
        confirmLabel="确认归档"
        onCancel={onCancel}
        onConfirm={onConfirm}
      />
    );

    expect(screen.getByRole("dialog", { name: "归档风格" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "取消" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("makes the background inert and restores the invoking control after close", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return <>
        <main data-testid="dialog-background">
          <button type="button" onClick={() => setOpen(true)}>打开确认</button>
          <button type="button">背景操作</button>
        </main>
        <ConfirmDialog
          open={open}
          title="删除记录"
          description="删除后无法恢复。"
          confirmLabel="确认删除"
          onCancel={() => setOpen(false)}
          onConfirm={vi.fn()}
        />
      </>;
    }
    const user = userEvent.setup();
    render(<Harness />);

    const trigger = screen.getByRole("button", { name: "打开确认" });
    await user.click(trigger);
    expect(screen.getByTestId("dialog-background")).toHaveAttribute("inert");
    expect(screen.getByTestId("dialog-background")).toHaveAttribute("aria-hidden", "true");
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog", { name: "删除记录" })).not.toBeInTheDocument();
    expect(screen.getByTestId("dialog-background")).not.toHaveAttribute("inert");
    expect(trigger).toHaveFocus();
  });
});
