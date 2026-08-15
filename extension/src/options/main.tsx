import { render } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { useDialogs } from "../components/Dialog";
import { ActionStatus, BusyLabel, type ActionFeedback } from "../components/Feedback";
import { buildProfileBackupFilename, exportProfileBackup, parseProfileBackup } from "../shared/profile/backup";
import { clearProfile, loadProfile, saveProfile } from "../shared/storage/chromeStorage";
import type { Profile } from "../shared/profile/types";
import "../styles/app.css";

type OptionsOperation = "export" | "import" | "reset";

function getErrorMessage(error: unknown, fallback: string) {
  return error instanceof Error && error.message ? error.message : fallback;
}

function OptionsApp() {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [profileLoading, setProfileLoading] = useState(true);
  const [feedback, setFeedback] = useState<ActionFeedback>({ tone: "info", message: "" });
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [importBusyText, setImportBusyText] = useState("正在校验…");
  const [resetting, setResetting] = useState(false);
  const activeOperationRef = useRef<OptionsOperation | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { confirm: confirmAction, node: dialogNode } = useDialogs();

  useEffect(() => {
    let active = true;

    void loadProfile()
      .then((loadedProfile) => {
        if (active) setProfile(loadedProfile);
      })
      .catch((error: unknown) => {
        if (!active) return;
        setFeedback({
          tone: "error",
          message: `读取配置失败：${getErrorMessage(error, "无法访问本地存储")}。请刷新页面后重试。`
        });
      })
      .finally(() => {
        if (active) setProfileLoading(false);
      });

    return () => {
      active = false;
    };
  }, []);

  async function exportConfig() {
    if (activeOperationRef.current) return;
    activeOperationRef.current = "export";
    setExporting(true);
    setFeedback({ tone: "pending", message: "正在生成配置文件…" });

    let objectUrl: string | null = null;
    try {
      const current = profile ?? (await loadProfile());
      const blob = new Blob([exportProfileBackup(current)], {
        type: "application/json;charset=utf-8"
      });
      objectUrl = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = objectUrl;
      anchor.download = buildProfileBackupFilename(current);
      anchor.click();
      setProfile(current);
      setFeedback({
        tone: "success",
        message: "已生成配置文件并发起下载。本地图片和缓存图标不会包含在配置文件中。"
      });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: `导出失败：${getErrorMessage(error, "无法生成配置文件")}。请重试。`
      });
    } finally {
      if (objectUrl) URL.revokeObjectURL(objectUrl);
      activeOperationRef.current = null;
      setExporting(false);
    }
  }

  async function importConfig(file: File | null) {
    if (!file || activeOperationRef.current) return;

    activeOperationRef.current = "import";
    setImporting(true);
    setImportBusyText("正在校验…");
    setFeedback({ tone: "pending", message: `正在读取并校验“${file.name}”…` });

    try {
      const current = profile ?? (await loadProfile());
      const imported = parseProfileBackup(await file.text(), current);
      const groupCount = imported.groups.filter((group) => !group.deletedAt).length;
      const shortcutCount = imported.shortcuts.filter((shortcut) => !shortcut.deletedAt).length;
      const accepted = await confirmAction({
        dedupeKey: "options-profile-import",
        title: "导入并覆盖当前配置？",
        description: (
          <>
            <p>文件已通过校验。继续后，将覆盖当前浏览器中的可同步配置。</p>
            <p>如果已连接后端，导入结果可能进入同步队列并影响其他浏览器。</p>
            <dl className="option-list">
              <div>
                <dt>文件</dt>
                <dd>{file.name}</dd>
              </div>
              <div>
                <dt>分组</dt>
                <dd>{groupCount} 个</dd>
              </div>
              <div>
                <dt>快捷方式</dt>
                <dd>{shortcutCount} 个</dd>
              </div>
            </dl>
          </>
        ),
        confirmLabel: "导入并覆盖",
        cancelLabel: "保留当前配置",
        tone: "warning"
      });

      if (!accepted) {
        setFeedback({ tone: "info", message: "已取消导入，当前配置未发生变化。" });
        return;
      }

      setImportBusyText("正在导入…");
      setFeedback({ tone: "pending", message: "正在写入导入的配置…" });
      await saveProfile(imported);
      setProfile(imported);
      setFeedback({ tone: "success", message: "配置已导入。刷新新标签页后会使用导入的数据。" });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: `导入失败：${getErrorMessage(error, "无法读取配置文件")}。当前配置未更改。`
      });
    } finally {
      activeOperationRef.current = null;
      setImporting(false);
    }
  }

  async function reset() {
    if (activeOperationRef.current) return;
    activeOperationRef.current = "reset";

    try {
      const accepted = await confirmAction({
        dedupeKey: "options-profile-reset",
        title: "重置本地配置？",
        description: (
          <>
            <p>这会清空当前快捷方式、分组和个性化设置，并恢复默认配置。</p>
            <p>配置标识、当前浏览器标识和同步连接会保留。</p>
            <p>如果已连接后端，重置结果可能进入同步队列并影响其他浏览器。</p>
          </>
        ),
        confirmLabel: "重置配置",
        cancelLabel: "取消",
        tone: "danger"
      });

      if (!accepted) {
        setFeedback({ tone: "info", message: "已取消重置，当前配置未发生变化。" });
        return;
      }

      setResetting(true);
      setFeedback({ tone: "pending", message: "正在重置本地配置…" });
      const next = await clearProfile();
      setProfile(next);
      setFeedback({ tone: "success", message: "本地配置已重置。" });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: `重置失败：${getErrorMessage(error, "无法写入本地存储")}。请重试。`
      });
    } finally {
      activeOperationRef.current = null;
      setResetting(false);
    }
  }

  const operationBusy = exporting || importing || resetting;
  const profileValue = (value: string | undefined) => value ?? (profileLoading ? "正在读取…" : "读取失败");

  return (
    <>
      <main className="options-page">
        <section className="options-shell">
          <p className="kicker">kekeio</p>
          <h1>选项</h1>
          <dl className="option-list">
            <div>
              <dt>配置标识</dt>
              <dd>{profileValue(profile?.profileId)}</dd>
            </div>
            <div>
              <dt>当前浏览器标识</dt>
              <dd>{profileValue(profile?.deviceId)}</dd>
            </div>
            <div>
              <dt>同步状态</dt>
              <dd>{profileValue(profile?.sync.status)}</dd>
            </div>
          </dl>
          <div className="option-actions" aria-label="配置备份">
            <button
              className="command primary"
              type="button"
              onClick={() => void exportConfig()}
              disabled={operationBusy}
              aria-busy={exporting || undefined}
            >
              <BusyLabel busy={exporting} busyText="正在导出…">导出配置</BusyLabel>
            </button>
            <button
              className="command import-command"
              type="button"
              onClick={() => fileInputRef.current?.click()}
              disabled={operationBusy}
              aria-busy={importing || undefined}
            >
              <BusyLabel busy={importing} busyText={importBusyText}>导入配置</BusyLabel>
            </button>
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json,.json"
              hidden
              disabled={operationBusy}
              onChange={(event) => {
                const file = event.currentTarget.files?.[0] ?? null;
                event.currentTarget.value = "";
                void importConfig(file);
              }}
            />
            <button
              className="command danger"
              type="button"
              onClick={() => void reset()}
              disabled={operationBusy}
              aria-busy={resetting || undefined}
            >
              <BusyLabel busy={resetting} busyText="正在重置…">重置本地配置</BusyLabel>
            </button>
          </div>
          <ActionStatus feedback={feedback} />
        </section>
      </main>
      {dialogNode}
    </>
  );
}

render(<OptionsApp />, document.getElementById("app")!);
