import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { buildProfileBackupFilename, exportProfileBackup, parseProfileBackup } from "../shared/profile/backup";
import { clearProfile, loadProfile, saveProfile } from "../shared/storage/chromeStorage";
import type { Profile } from "../shared/profile/types";
import "../styles/app.css";

function OptionsApp() {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [status, setStatus] = useState("");

  useEffect(() => {
    void loadProfile().then(setProfile);
  }, []);

  async function exportConfig() {
    const current = profile ?? (await loadProfile());
    const blob = new Blob([exportProfileBackup(current)], {
      type: "application/json;charset=utf-8"
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = buildProfileBackupFilename(current);
    anchor.click();
    URL.revokeObjectURL(url);
    setProfile(current);
    setStatus("已导出配置文件。本地图片和缓存图标不会包含在配置文件中。");
  }

  async function importConfig(files: FileList | null) {
    const file = files?.[0];
    if (!file) return;

    if (!window.confirm("导入配置会覆盖当前本地配置，确定继续？")) {
      setStatus("已取消导入。");
      return;
    }

    try {
      const current = profile ?? (await loadProfile());
      const imported = parseProfileBackup(await file.text(), current);
      await saveProfile(imported);
      setProfile(imported);
      setStatus("已导入配置。刷新新标签页后会使用导入的数据。");
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "导入失败");
    }
  }

  async function reset() {
    if (!window.confirm("确定重置本地配置？这会清空当前快捷方式、分组和设置。")) {
      setStatus("已取消重置。");
      return;
    }

    await clearProfile();
    const next = await loadProfile();
    setProfile(next);
    setStatus("已重置本地配置。");
  }

  return (
    <main className="options-page">
      <section className="options-shell">
        <p className="kicker">KeKeIO Tab</p>
        <h1>选项</h1>
        <dl className="option-list">
          <div>
            <dt>Profile</dt>
            <dd>{profile?.profileId ?? "loading"}</dd>
          </div>
          <div>
            <dt>Device</dt>
            <dd>{profile?.deviceId ?? "loading"}</dd>
          </div>
          <div>
            <dt>Sync</dt>
            <dd>{profile?.sync.status ?? "loading"}</dd>
          </div>
        </dl>
        <div className="option-actions" aria-label="配置备份">
          <button className="command primary" type="button" onClick={() => void exportConfig()}>
            导出配置
          </button>
          <label className="command import-command">
            导入配置
            <input
              type="file"
              accept="application/json,.json"
              onChange={(event) => {
                void importConfig(event.currentTarget.files);
                event.currentTarget.value = "";
              }}
            />
          </label>
          <button className="command danger" type="button" onClick={() => void reset()}>
            重置本地配置
          </button>
        </div>
        {status ? <p className="option-status">{status}</p> : null}
      </section>
    </main>
  );
}

render(<OptionsApp />, document.getElementById("app")!);
