// 左栏:设计稿 sidebar 的结构——品牌行(新建按钮)、全部下载/已完成、
// 分类导航(带数量)、网络速度卡、设置入口。移动端是抽屉(open 控制)。

import type { Item, Settings } from "../api";
import { Icon } from "../icons";
import { SpeedGraph } from "./SpeedGraph";
import { CATS, CAT_LABELS, parts } from "../util";

export type View = "all" | "completed" | "settings" | string; // 或类别名

export function Sidebar({
  items,
  view,
  speedHistory,
  settings,
  open,
  onView,
  onSettings,
  onAdd,
  onClose,
}: {
  items: Item[];
  view: View;
  speedHistory: number[];
  settings: Settings;
  /** 移动端抽屉展开态(桌面端忽略) */
  open?: boolean;
  onView: (v: View) => void;
  onSettings: () => void;
  onAdd: () => void;
  /** 移动端关闭抽屉 */
  onClose?: () => void;
}) {
  const counts: Record<string, number> = {};
  for (const it of items) counts[it.category] = (counts[it.category] || 0) + 1;
  const done = items.filter((it) => it.status === "completed").length;
  const total = items.reduce((s, it) => s + (it.status === "downloading" ? it.speed : 0), 0);
  const [n, unit] = parts(total);
  const active = items.filter((it) => it.status === "downloading").length;
  const limited = settings.speedLimitKib > 0;

  return (
    <aside className={`sidebar${open ? " sidebar-open" : ""}`}>
      <div className="brand-row">
        <div className="logo">
          <span>⇣</span>
        </div>
        <strong>SwiftDL</strong>
        <div className="side-actions">
          <button className="new-button" onClick={onAdd} aria-label="新建下载" title="新建下载 (Ctrl N)">
            <Icon n="plus" size={20} />
          </button>
        </div>
        <button className="icon-button quiet mobile-close" onClick={onClose} aria-label="关闭菜单">
          <Icon n="x" size={18} />
        </button>
      </div>

      <nav className="side-nav">
        <button
          className={`nav-item${view === "all" ? " active" : ""}`}
          onClick={() => onView("all")}
        >
          <Icon n="download" />
          <span>全部下载</span>
          <em>{items.length}</em>
        </button>
        <button
          className={`nav-item${view === "completed" ? " active" : ""}`}
          onClick={() => onView("completed")}
        >
          <Icon n="check" />
          <span>已完成</span>
          <em>{done}</em>
        </button>
        <p className="nav-label">分类</p>
        {Object.keys(CATS).map((cat) => (
          <button
            key={cat}
            className={`nav-item${view === cat ? " active" : ""}`}
            onClick={() => onView(cat)}
            style={{ display: counts[cat] || view === cat ? "" : "none" }}
          >
            <Icon n={CATS[cat].icon} />
            <span>{CAT_LABELS[cat]}</span>
            <em>{counts[cat] || ""}</em>
          </button>
        ))}
      </nav>

      <div className="sidebar-footer">
        <section className="network-card">
          <div className="network-heading">
            <span>网络</span>
            <button
              className={`speed-icon${limited ? " on" : ""}`}
              onClick={onSettings}
              title={limited ? `限速 ${settings.speedLimitKib} KiB/s,点击调整` : "限速设置"}
            >
              <Icon n="gauge" size={17} />
            </button>
          </div>
          <div className="speed-number">
            {n} <small>{unit}/s</small>
          </div>
          <SpeedGraph history={speedHistory} id="net-graph" />
          <div className="space-line">
            任务 <span>{active > 0 ? `${active} 个进行中` : "空闲"}</span>
          </div>
        </section>
        <button
          className={`settings-link${view === "settings" ? " selected" : ""}`}
          onClick={onSettings}
        >
          <Icon n="settings" size={18} /> 设置
        </button>
      </div>
    </aside>
  );
}
