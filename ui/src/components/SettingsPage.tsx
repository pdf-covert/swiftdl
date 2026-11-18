// 设置页:设计稿 settings-page——分节卡片(下载/网络/BitTorrent/数据)。
// 不再是弹窗,改一项存一项;下载中时连接类项目禁用。

import { useEffect, useRef, useState } from "react";
import { api, type DiagRow, type Settings } from "../api";
import { Icon } from "../icons";
import { FolderPicker } from "./FolderPicker";

export function SettingsPage({
  settings,
  version,
  busy,
  dev,
  onChange,
  onClearCompleted,
}: {
  settings: Settings;
  version: string;
  /** 有任务正在下载:连接数改了对它们无效,直接禁用。 */
  busy: boolean;
  /** 本地开发模式:下载目录固定,不显示「更改」按钮。 */
  dev?: boolean;
  /** 任意一项变化时整份保存。 */
  onChange: (s: Settings) => void;
  onClearCompleted: () => void;
}) {
  const [draft, setDraft] = useState<Settings>(settings);
  const [picking, setPicking] = useState(false);
  const [limitUnit, setLimitUnit] = useState<"KiB" | "MiB">("KiB");
  const [diag, setDiag] = useState<DiagRow[] | null>(null);
  const [diagBusy, setDiagBusy] = useState(false);
  // 轮询每拍都会给 settings 换新对象;只有值真的变了(且不是自己刚存的那份)才重置草稿,
  // 否则正在输入的代理地址会被进度刷新冲掉。
  const lastSaved = useRef<Settings>(settings);

  useEffect(() => {
    if (JSON.stringify(settings) === JSON.stringify(lastSaved.current)) return;
    lastSaved.current = settings;
    setDraft(settings);
  }, [settings]);

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => {
    const next = { ...draft, [k]: v };
    setDraft(next);
    lastSaved.current = next;
    onChange(next);
  };

  const switchRow = (
    label: string,
    hint: string,
    key: "sortIntoCategories",
  ) => (
    <div className="setting-row">
      <div>
        <strong>{label}</strong>
        <span>{hint}</span>
      </div>
      <button
        type="button"
        className={`switch${draft[key] ? " on" : ""}`}
        onClick={() => set(key, !draft[key] as never)}
        aria-label={`切换${label}`}
      >
        <i />
      </button>
    </div>
  );

  const stepperRow = (
    label: string,
    hint: string,
    key: "connections" | "maxConcurrent",
    min: number,
    max: number,
  ) => (
    <div className={`setting-row${busy ? " off" : ""}`}>
      <div>
        <strong>{label}</strong>
        <span className={busy ? "warn" : ""}>{busy ? "有任务正在下载,暂停后再改" : hint}</span>
      </div>
      <span className="stepper">
        <button type="button" disabled={busy} onClick={() => set(key, Math.max(min, draft[key] - 1) as never)}>
          −
        </button>
        <input
          value={draft[key]}
          disabled={busy}
          onChange={(e) => set(key, Math.max(min, Math.min(max, Number(e.target.value) || min)) as never)}
        />
        <button type="button" disabled={busy} onClick={() => set(key, Math.min(max, draft[key] + 1) as never)}>
          +
        </button>
      </span>
    </div>
  );

  const limited = draft.speedLimitKib > 0;

  return (
    <>
      <div className="settings-page">
        <div className="settings-intro">
          <p>管理下载偏好、网络与应用行为。</p>
          <span>SwiftDL {version || "1.0"}</span>
        </div>

        <section className="settings-section">
          <h2>下载</h2>
          <div className="setting-card">
            <div className="setting-row">
              <div>
                <strong>下载位置</strong>
                <span>{draft.downloadDir ? "新任务默认保存的位置" : "必须选一个 fnOS 已授权的共享文件夹"}</span>
              </div>
              <label className="setting-path">
                <Icon n="doc" size={17} />
                <input readOnly value={draft.downloadDir} placeholder="选择(需授权)" title={draft.downloadDir} />
                {!dev && (
                  <button type="button" onClick={() => setPicking(true)}>
                    更改
                  </button>
                )}
              </label>
            </div>
            {stepperRow("每个任务的连接数", "分多少条连接同时下", "connections", 1, 32)}
            {stepperRow("同时进行的任务", "其余的排队等", "maxConcurrent", 1, 10)}
            {switchRow("按类型分文件夹", "视频、音乐、程序…各归各类", "sortIntoCategories")}
          </div>
        </section>

        <section className="settings-section">
          <h2>网络</h2>
          <div className="setting-card">
            <div className="setting-row">
              <div>
                <strong>限速下载</strong>
                <span>限制所有任务的最大下载速度</span>
              </div>
              <button
                type="button"
                className={`switch${limited ? " on" : ""}`}
                onClick={() => set("speedLimitKib", limited ? 0 : 10 * 1024)}
                aria-label="切换限速下载"
              >
                <i />
              </button>
            </div>
            {limited && (
              <div className="speed-limit">
                <span>最大下载速度</span>
                <input
                  inputMode="decimal"
                  value={
                    limitUnit === "MiB"
                      ? Math.round((draft.speedLimitKib / 1024) * 10) / 10
                      : draft.speedLimitKib
                  }
                  onChange={(e) => {
                    const v = Math.max(0, Number(e.target.value) || 0);
                    set("speedLimitKib", limitUnit === "MiB" ? Math.round(v * 1024) : Math.round(v));
                  }}
                />
                <em
                  className={limitUnit === "KiB" ? "" : "dim"}
                  onClick={() => setLimitUnit("KiB")}
                  title="切到 KiB/s"
                >
                  KiB/s
                </em>
                <em
                  className={limitUnit === "MiB" ? "" : "dim"}
                  onClick={() => setLimitUnit("MiB")}
                  title="切到 MiB/s"
                >
                  MiB/s
                </em>
              </div>
            )}
            <div className="setting-row set-col">
              <div>
                <strong>代理</strong>
                <span>如 http://192.168.1.2:7890;留空直连。重启应用后生效</span>
              </div>
              <input
                className="txt"
                value={draft.proxy}
                onChange={(e) => set("proxy", e.target.value)}
                placeholder="http://..."
                spellCheck={false}
              />
            </div>
          </div>
        </section>

        <section className="settings-section">
          <h2>网络诊断</h2>
          <div className="setting-card">
            <div className="setting-row">
              <div>
                <strong>BT 找源三要素</strong>
                <span>DNS 解析、UDP tracker、TCP 出站逐条点亮,下载慢时先看这里</span>
              </div>
              <button
                type="button"
                className="outline-button"
                disabled={diagBusy}
                onClick={() => {
                  setDiagBusy(true);
                  api
                    .netDiag()
                    .then(setDiag)
                    .catch((e) => setDiag([{ name: "检测失败", ok: false, detail: String(e) }]))
                    .finally(() => setDiagBusy(false));
                }}
              >
                {diagBusy ? "检测中…" : diag ? "再测一次" : "开始检测"}
              </button>
            </div>
            {diag && (
              <div className="diag-rows">
                {diag.map((r) => (
                  <div className="diag-row" key={r.name}>
                    <i className={`diag-dot${r.ok === null ? " info" : r.ok ? " ok" : " bad"}`} />
                    <b>{r.name}</b>
                    <span title={r.detail}>{r.detail}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>

        <section className="settings-section">
          <h2>BitTorrent</h2>
          <div className="setting-card">
            <div className="setting-row">
              <div>
                <strong>下载完成后</strong>
                <span>做种多久:到分享率或一直做</span>
              </div>
              <span className="seg">
                {[
                  ["stop", "停止"],
                  ["1", "到 1.0"],
                  ["2", "到 2.0"],
                  ["forever", "一直"],
                ].map(([v, label]) => (
                  <button
                    key={v}
                    type="button"
                    className={draft.seedUntil === v ? "on" : ""}
                    onClick={() => set("seedUntil", v)}
                  >
                    {label}
                  </button>
                ))}
              </span>
            </div>
          </div>
        </section>

        <section className="settings-section">
          <h2>外观</h2>
          <div className="setting-card">
            <div className="setting-row">
              <div>
                <strong>主题</strong>
                <span>跟随系统或固定一边</span>
              </div>
              <span className="seg">
                {["system", "light", "dark"].map((t) => (
                  <button
                    key={t}
                    type="button"
                    className={(draft.theme || "system") === t ? "on" : ""}
                    onClick={() => set("theme", t)}
                  >
                    {t === "system" ? "跟随系统" : t === "light" ? "浅色" : "深色"}
                  </button>
                ))}
              </span>
            </div>
            <div className="setting-row">
              <div>
                <strong>界面缩放</strong>
                <span>{draft.uiScale}%</span>
              </div>
              <input
                type="range"
                min={90}
                max={150}
                step={5}
                value={draft.uiScale}
                onChange={(e) => set("uiScale", Number(e.target.value))}
                style={{ width: 160 }}
              />
            </div>
          </div>
        </section>

        <section className="settings-section">
          <h2>数据</h2>
          <div className="setting-card setting-row">
            <div>
              <strong>清除已完成记录</strong>
              <span>不会删除已经下载的文件</span>
            </div>
            <button className="outline-button" onClick={onClearCompleted}>
              清除记录
            </button>
          </div>
        </section>
      </div>
      <FolderPicker
        open={picking}
        start={draft.downloadDir || null}
        onPick={(p) => {
          set("downloadDir", p);
          setPicking(false);
        }}
        onClose={() => setPicking(false)}
      />
    </>
  );
}
