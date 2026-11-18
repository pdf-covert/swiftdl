// 应用外壳:设计稿 app-shell——侧栏 + 白色工作区(工具栏 / 列表+详情 /
// 设置页),移动端是抽屉 + 顶栏 + 底部导航。轮询、快捷键、右键菜单、
// 各对话框与完成通知沿用。

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, type Item } from "./api";
import { useAppState } from "./hooks";
import { Icon } from "./icons";
import { bytes } from "./util";
import { CATS, CAT_LABELS } from "./util";
import { Composer } from "./components/Composer";
import { Dialog } from "./components/Dialog";
import { Inspector } from "./components/Inspector";
import { Row, SectionHead, GROUP_TITLES } from "./components/Row";
import { SettingsPage } from "./components/SettingsPage";
import { Sidebar, type View } from "./components/Sidebar";
import { Toasts, useToasts } from "./components/Toasts";

export default function App() {
  const state = useAppState();
  const { list: toasts, toast, gone } = useToasts();
  const [view, setView] = useState<View>("all");
  const [selected, setSelected] = useState<number | null>(null);
  const [composerOpen, setComposerOpen] = useState(false);
  // 移动端侧边抽屉:点汉堡打开,点遮罩/选完菜单项自动收起
  const [menuOpen, setMenuOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<{ ids: number[]; live: boolean } | null>(null);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [setUrlFor, setSetUrlFor] = useState<Item | null>(null);
  const [newUrl, setNewUrl] = useState("");
  const [menu, setMenu] = useState<{ x: number; y: number; item: Item } | null>(null);
  const [speedHistory, setSpeedHistory] = useState<number[]>([]);
  const lastTick = useRef(0);

  const items = state?.items ?? [];
  const settings = state?.settings ?? null;

  // 速度历史:每拍(版本号变化)记一次,侧栏网速卡用,留 60 点。
  useEffect(() => {
    if (!state) return;
    if (lastTick.current === state.v) return;
    lastTick.current = state.v;
    const total = state.items.reduce((s, it) => s + (it.status === "downloading" ? it.speed : 0), 0);
    setSpeedHistory((h) => [...h.slice(-59), total]);
  }, [state]);

  // 主题(深色模式保留)。
  useEffect(() => {
    const apply = () => {
      const want =
        settings?.theme === "dark" || settings?.theme === "light"
          ? settings.theme
          : window.matchMedia("(prefers-color-scheme: dark)").matches
            ? "dark"
            : "light";
      document.documentElement.dataset.theme = want;
    };
    apply();
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, [settings?.theme]);

  // 界面缩放。
  useEffect(() => {
    if (settings?.uiScale) document.documentElement.style.fontSize = `${(settings.uiScale / 100) * 16}px`;
  }, [settings?.uiScale]);

  const isSettings = view === "settings";
  const visible = useMemo(
    () =>
      view === "all" || isSettings
        ? items
        : view === "completed"
          ? items.filter((it) => it.status === "completed")
          : items.filter((it) => it.category === view),
    [items, view, isSettings],
  );

  const groups = useMemo(() => {
    const queueOrder = new Map(items.map((it, n) => [it.id, n]));
    return GROUP_TITLES.map(([s]) => {
      const list = visible.filter((it) => it.status === s);
      list.sort(
        s === "completed"
          ? (a, b) => (b.completedAt || 0) - (a.completedAt || 0)
          : s === "queued"
            ? (a, b) => (queueOrder.get(a.id) || 0) - (queueOrder.get(b.id) || 0)
            : (a, b) => b.addedAt - a.addedAt,
      );
      return { s, list };
    }).filter((g) => g.list.length);
  }, [visible, items]);

  const queuePos = useCallback(
    (id: number) => {
      const order = items.filter((it) => it.status === "queued").map((it) => it.id);
      return order.indexOf(id) + 1;
    },
    [items],
  );

  const selectedItem = items.find((it) => it.id === selected) || null;

  const pauseResume = async (it: Item) => {
    try {
      if (it.status === "downloading" || it.status === "queued") await api.pause([it.id]);
      else await api.resume([it.id]);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  };

  const doRemove = async () => {
    if (!confirmDelete) return;
    try {
      await api.remove(confirmDelete.ids, deleteFiles);
      if (selected && confirmDelete.ids.includes(selected)) setSelected(null);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    } finally {
      setConfirmDelete(null);
      setDeleteFiles(false);
    }
  };

  // 快捷键。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const inInput = (e.target as HTMLElement)?.matches?.("input, textarea, select") ?? false;
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "n") {
        e.preventDefault();
        setComposerOpen(true);
      } else if (e.key === "Delete" && selected && !inInput) {
        const it = items.find((i) => i.id === selected);
        if (it) setConfirmDelete({ ids: [it.id], live: it.status === "downloading" || it.status === "queued" });
      } else if (e.key === " " && selected && !inInput) {
        e.preventDefault();
        const it = items.find((i) => i.id === selected);
        if (it && it.status !== "completed") pauseResume(it);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  useEffect(() => {
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
    };
  }, []);

  const viewLabel = isSettings
    ? "设置"
    : view === "all"
      ? "全部下载"
      : view === "completed"
        ? "已完成"
        : CAT_LABELS[view] || view;
  const viewIcon = isSettings
    ? "settings"
    : view === "all"
      ? "download"
      : view === "completed"
        ? "check"
        : CATS[view]?.icon || "folder";

  // 首拍数据没到之前给个安静的加载面(Sidebar 等组件要求 settings 非空)。
  if (!settings) return <div className="boot">正在连接…</div>;

  return (
    <div className="app-shell">
      <Sidebar
        items={items}
        view={view}
        open={menuOpen}
        onView={(v) => {
          setView(v);
          setMenuOpen(false);
        }}
        speedHistory={speedHistory}
        settings={settings}
        onSettings={() => {
          setView("settings");
          setMenuOpen(false);
        }}
        onAdd={() => {
          setComposerOpen(true);
          setMenuOpen(false);
        }}
        onClose={() => setMenuOpen(false)}
      />

      <section className="workspace">
        <header className="mobile-header">
          <button className="icon-button quiet" onClick={() => setMenuOpen(true)} aria-label="打开菜单">
            <Icon n="menu" size={20} />
          </button>
          <div className="mobile-brand">
            <div className="logo small">
              <span>⇣</span>
            </div>
            <b>SwiftDL</b>
          </div>
          <button className="icon-button add-round" onClick={() => setComposerOpen(true)} aria-label="新建下载">
            <Icon n="plus" size={20} />
          </button>
        </header>

        <div className={`toolbar${isSettings ? " settings-toolbar" : ""}`}>
          <div className="page-title">
            <Icon n={viewIcon} size={21} />
            <strong>{viewLabel}</strong>
            {!isSettings && <span>{visible.length}</span>}
          </div>
          {!isSettings && (
            <div className="toolbar-actions">
              <button
                onClick={() =>
                  api
                    .pause(items.filter((i) => i.status === "downloading" || i.status === "queued").map((i) => i.id))
                    .catch(() => {})
                }
              >
                <Icon n="pause" size={17} /> 全部暂停
              </button>
              <button
                onClick={() =>
                  api
                    .resume(items.filter((i) => i.status === "paused" || i.status === "error").map((i) => i.id))
                    .catch(() => {})
                }
              >
                <Icon n="play" size={17} /> 全部继续
              </button>
            </div>
          )}
        </div>

        {isSettings ? (
          <SettingsPage
            settings={settings}
            version={state?.version ?? ""}
            busy={items.some((i) => i.status === "downloading")}
            dev={!!state?.dev}
            onChange={async (s) => {
              try {
                await api.setSettings(s);
              } catch (e) {
                toast(e instanceof Error ? e.message : String(e), "err");
              }
            }}
            onClearCompleted={() =>
              setConfirmDelete({
                ids: items.filter((i) => i.status === "completed").map((i) => i.id),
                live: false,
              })
            }
          />
        ) : (
          <div className="downloads-area">
            <div className="content">
              {settings.downloadDir === "" && visible.length > 0 && (
                <button className="dir-banner" onClick={() => setView("settings")}>
                  <Icon n="lock" size={14} />
                  还没有选择下载目录——点这里,选一个 fnOS 已授权的共享文件夹
                </button>
              )}
              <div
                className="list download-list"
                onClick={(e) => !(e.target as HTMLElement).closest(".download-row") && setSelected(null)}
              >
                {groups.map((g) => (
                  <div key={g.s}>
                    <SectionHead
                      status={g.s}
                      count={g.list.length}
                      right={
                        g.s === "downloading"
                          ? bytes(g.list.reduce((s, i) => s + i.speed, 0)) + "/s"
                          : g.s === "completed"
                            ? bytes(g.list.reduce((s, i) => s + (i.size || 0), 0))
                            : ""
                      }
                      onClear={
                        g.s === "completed" ? () => setConfirmDelete({ ids: g.list.map((i) => i.id), live: false }) : undefined
                      }
                    />
                    {g.list.map((it) => (
                      <Row
                        key={it.id}
                        item={it}
                        queuePos={queuePos(it.id)}
                        selected={selected === it.id}
                        onSelect={() => setSelected(it.id)}
                        onPauseResume={() => pauseResume(it)}
                        onRemove={() =>
                          setConfirmDelete({
                            ids: [it.id],
                            live: it.status === "downloading" || it.status === "queued",
                          })
                        }
                        onMenu={(e) => {
                          e.preventDefault();
                          setMenu({ x: e.clientX, y: e.clientY, item: it });
                        }}
                      />
                    ))}
                  </div>
                ))}
                {visible.length === 0 && (
                  <div className="empty-state">
                    <div className="empty-icon">
                      <Icon n="search" size={28} />
                    </div>
                    <h2>{items.length ? "这里什么都没有" : "还没有下载"}</h2>
                    <span>{items.length ? "换个分类或关键词看看" : "粘贴一个链接、磁力链接,或一次贴一批"}</span>
                    {settings.downloadDir === "" && (
                      <button className="dir-banner in-empty" onClick={() => setView("settings")}>
                        <Icon n="lock" size={14} />
                        还没有选择下载目录——点这里,选一个 fnOS 已授权的共享文件夹
                      </button>
                    )}
                    <div className="empty-actions">
                      <button
                        className="outline-button"
                        onClick={() => {
                          setView("all");
                          setComposerOpen(true);
                        }}
                      >
                        <Icon n="plus" size={15} />
                        新建下载
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </div>
            {selectedItem && (
              <Inspector
                item={selectedItem}
                onClose={() => setSelected(null)}
                onPauseResume={() => pauseResume(selectedItem)}
                onRemove={() =>
                  setConfirmDelete({
                    ids: [selectedItem.id],
                    live: selectedItem.status === "downloading" || selectedItem.status === "queued",
                  })
                }
                onVerifyHint={async () => {
                  // 重新入队完成即再校验一次。
                  try {
                    await api.resume([selectedItem.id]);
                    toast("已重新加入队列,完成时会再次校验");
                  } catch (e) {
                    toast(e instanceof Error ? e.message : String(e), "err");
                  }
                }}
              />
            )}
          </div>
        )}
      </section>

      <div className={`scrim${menuOpen ? " visible" : ""}`} onClick={() => setMenuOpen(false)} />
      <nav className="mobile-nav">
        <button className={!isSettings ? "active" : ""} onClick={() => setView("all")}>
          <Icon n="download" />
          <span>下载</span>
        </button>
        <button className="new-download" onClick={() => setComposerOpen(true)} aria-label="新建下载">
          <span>
            <Icon n="plus" size={18} />
          </span>
        </button>
        <button className={isSettings ? "active" : ""} onClick={() => setView("settings")}>
          <Icon n="settings" />
          <span>设置</span>
        </button>
      </nav>

      {menu && (
        <div
          className="menu"
          style={{
            left: Math.max(8, Math.min(menu.x, window.innerWidth - 216)),
            top: Math.max(8, Math.min(menu.y, window.innerHeight - 320)),
          }}
          onClick={(e) => e.stopPropagation()}
        >
          {menu.item.status !== "completed" && (
            <button onClick={() => (pauseResume(menu.item), setMenu(null))}>
              <Icon n={menu.item.status === "downloading" || menu.item.status === "queued" ? "pause" : "play"} size={16} />
              {menu.item.status === "downloading" || menu.item.status === "queued"
                ? "暂停"
                : menu.item.status === "error"
                  ? "重试"
                  : "继续"}
            </button>
          )}
          {menu.item.status === "queued" && (
            <button onClick={() => (api.downloadNext(menu.item.id).catch(() => {}), setMenu(null))}>
              <Icon n="arrowUp" size={16} />
              提到队首
            </button>
          )}
          <button
            onClick={() => (
              navigator.clipboard.writeText(menu.item.url).catch(() => {}),
              toast("链接已复制"),
              setMenu(null)
            )}
          >
            <Icon n="copy" size={16} />
            复制链接
          </button>
          {!menu.item.torrentFiles && menu.item.status !== "completed" && menu.item.status !== "downloading" && (
            <button onClick={() => (setSetUrlFor(menu.item), setNewUrl(menu.item.url), setMenu(null))}>
              <Icon n="link" size={16} />
              更换地址…
            </button>
          )}
          <div className="msep" />
          <button
            className="danger"
            onClick={() => (
              setConfirmDelete({
                ids: [menu.item.id],
                live: menu.item.status === "downloading" || menu.item.status === "queued",
              }),
              setMenu(null)
            )}
          >
            <Icon n="trash" size={16} />
            删除…
          </button>
        </div>
      )}

      <Composer
        open={composerOpen}
        settings={settings}
        dev={!!state?.dev}
        onClose={() => setComposerOpen(false)}
        onAdded={(m) => toast(m)}
        onError={(m) => toast(m, "err")}
      />

      <Dialog open={!!confirmDelete} onClose={() => setConfirmDelete(null)} className="modal confirm">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            doRemove();
          }}
        >
          <h3>删除下载任务？</h3>
          <p>
            {confirmDelete?.ids.length === 1
              ? "从列表移除这个任务。"
              : `从列表移除 ${confirmDelete?.ids.length} 个任务。`}
            {confirmDelete?.live ? "进行中的会先停下。" : "此操作无法撤销。"}
          </p>
          {confirmDelete && (
            <label className="delete-file-option">
              <input type="checkbox" checked={deleteFiles} onChange={(e) => setDeleteFiles(e.target.checked)} />
              <span>
                <Icon n="check" size={14} />
              </span>
              <strong>同时删除本地文件</strong>
            </label>
          )}
          <div className="delete-dialog-actions">
            <button type="button" onClick={() => setConfirmDelete(null)}>
              取消
            </button>
            <button type="submit" className="delete-confirm">
              删除
            </button>
          </div>
        </form>
      </Dialog>

      <Dialog open={!!setUrlFor} onClose={() => setSetUrlFor(null)} className="modal confirm">
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            if (!setUrlFor) return;
            try {
              await api.setUrl(setUrlFor.id, newUrl.trim());
              toast("已换地址并继续");
              setSetUrlFor(null);
            } catch (err) {
              toast(err instanceof Error ? err.message : String(err), "err");
            }
          }}
        >
          <h3>更换地址</h3>
          <p>链接过期时给同一文件换个新地址,已下载的部分继续用。</p>
          <input
            className="txt"
            value={newUrl}
            onChange={(e) => setNewUrl(e.target.value)}
            spellCheck={false}
          />
          <div className="modal-footer">
            <button type="button" className="cancel-button" onClick={() => setSetUrlFor(null)}>
              取消
            </button>
            <button type="submit" className="submit-button">
              换掉并继续
            </button>
          </div>
        </form>
      </Dialog>

      <Toasts list={toasts} onGone={gone} />
    </div>
  );
}
