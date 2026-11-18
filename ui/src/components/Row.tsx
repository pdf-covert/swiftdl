// 列表行与分节头:设计稿 download-row 的结构——类型色块、两行截断的
// 文件名、按状态各不相同的进度摘要列、行尾操作;触屏长按呼出右键菜单。

import { useRef } from "react";
import type { Item, Status } from "../api";
import { FileKind, Icon } from "../icons";
import { bytes, dur, speed as fmtSpeed } from "../util";

const pct = (it: { size: number | null; downloaded: number }) =>
  it.size ? Math.min(100, (it.downloaded / it.size) * 100) : 0;

export const GROUP_TITLES: [Status, string][] = [
  ["downloading", "正在下载"],
  ["queued", "等待中"],
  ["paused", "已暂停"],
  ["error", "失败"],
  ["completed", "已完成"],
];

export function SectionHead({
  status,
  count,
  right,
  onClear,
}: {
  status: Status;
  count: number;
  right: string;
  onClear?: () => void;
}) {
  return (
    <div className="status-heading">
      <strong>{GROUP_TITLES.find(([s]) => s === status)?.[1]}</strong>
      <span>{count}</span>
      {right && <span className="gr">{right}</span>}
      {status === "completed" && onClear && (
        <button type="button" className="gclear" onClick={onClear} title="把已完成的从列表移除(文件保留)">
          清除
        </button>
      )}
    </div>
  );
}

export function Row({
  item,
  queuePos,
  selected,
  onSelect,
  onPauseResume,
  onRemove,
  onMenu,
}: {
  item: Item;
  queuePos: number;
  selected: boolean;
  onSelect: () => void;
  onPauseResume: () => void;
  onRemove: () => void;
  onMenu: (e: React.MouseEvent) => void;
}) {
  const p = pct(item);
  const live = item.status === "downloading";
  const queued = item.status === "queued";
  const finished = item.status === "completed";
  const failed = item.status === "error";
  const total = item.size ? bytes(item.size) : "—";
  // 触屏长按(500ms)呼出右键菜单;松手后紧跟的 click 吞掉,不开详情。
  const press = useRef(0);
  const ateClick = useRef(false);

  const summaryBar = (width: number) => <i><b style={{ width: `${width}%` }} /></i>;

  return (
    <article
      className={`download-row${selected ? " selected" : ""}`}
      onClick={() => {
        if (ateClick.current) {
          ateClick.current = false;
          return;
        }
        onSelect();
      }}
      onContextMenu={onMenu}
      onPointerDown={(ev) => {
        if (ev.pointerType === "mouse") return;
        const x = ev.clientX;
        const y = ev.clientY;
        clearTimeout(press.current);
        press.current = window.setTimeout(() => {
          ateClick.current = true;
          onMenu({ clientX: x, clientY: y } as unknown as React.MouseEvent);
        }, 500);
      }}
      onPointerMove={() => clearTimeout(press.current)}
      onPointerUp={() => clearTimeout(press.current)}
      onPointerCancel={() => clearTimeout(press.current)}
    >
      <FileKind name={item.filename} torrent={!!item.torrentFiles} category={item.category} />
      <div className="download-main">
        <div className="file-title" title={item.filename}>
          {item.filename}
          {failed && <span className="r-err"> · {item.error || "失败"}</span>}
        </div>
        <div className="mobile-meta">
          <span>
            {finished ? total : `${bytes(item.downloaded)} / ${total}`}
          </span>
          {live ? (
            <span className="mobile-speed">
              <Icon n="download" size={12} />
              {item.speed > 0 ? fmtSpeed(item.speed) : item.note || "连接中…"}
            </span>
          ) : (
            <span className={`mobile-status${item.status === "paused" ? " paused" : ""}${failed ? " err" : ""}`}>
              {finished
                ? `完成于 ${item.size ? bytes(item.size) : ""}`
                : queued
                  ? `排队 · 第 ${queuePos} 位`
                  : failed
                    ? "失败"
                    : "已暂停"}
            </span>
          )}
        </div>
        <div className="progress-track">
          <span
            className={item.status === "paused" ? "paused" : failed ? "err" : ""}
            style={{ width: `${p}%` }}
          />
        </div>
      </div>

      {live || queued ? (
        <>
          <div className="running-summary">
            <div>
              <span>
                {bytes(item.downloaded)} / {total}
              </span>
              <span>
                {live
                  ? item.size && item.speed > 0
                    ? dur((item.size - item.downloaded) / item.speed)
                    : item.note || "连接中…"
                  : `排队 · 第 ${queuePos} 位`}
              </span>
            </div>
            {summaryBar(p)}
          </div>
          <span className="row-speed">{live ? (item.speed > 0 ? fmtSpeed(item.speed) : "") : ""}</span>
          <button
            className="row-state"
            aria-label="暂停下载"
            title="暂停"
            onClick={(ev) => {
              ev.stopPropagation();
              onPauseResume();
            }}
          >
            <Icon n="pause" size={19} />
          </button>
        </>
      ) : finished ? (
        <>
          <div className="paused-summary">
            <span>{total}</span>
            {summaryBar(100)}
          </div>
          <span className="row-paused">已完成</span>
        </>
      ) : (
        <>
          <div className="paused-summary">
            <span>
              {bytes(item.downloaded)} / {total}
            </span>
            {summaryBar(p)}
          </div>
          <span className={`row-paused${failed ? " err" : ""}`} title={item.error || undefined}>
            {failed ? "失败" : "已暂停"}
          </span>
          <button
            className="row-resume"
            aria-label={failed ? "重试下载" : "继续下载"}
            title={failed ? "重试" : "继续"}
            onClick={(ev) => {
              ev.stopPropagation();
              onPauseResume();
            }}
          >
            <Icon n="download" size={23} />
          </button>
        </>
      )}
      <button
        className="row-close"
        aria-label="移除下载任务"
        title="删除"
        onClick={(ev) => {
          ev.stopPropagation();
          onRemove();
        }}
      >
        <Icon n="x" size={20} />
      </button>
    </article>
  );
}
