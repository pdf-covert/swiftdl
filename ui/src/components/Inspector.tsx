// 详情面板:设计稿 download-detail——文件头、进度、种子文件列表、属性行、
// 底部操作。用 Radix Dialog 做成抽屉:桌面从右侧滑出盖在列表上(不再挤压
// 列表),移动端从底部滑出(CSS 控制)。Esc / 点遮罩 / 点右上角都能关。

import { useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { api, type Item } from "../api";
import { FileKind, Icon } from "../icons";
import { bytes, host, parseEd2k, speed as fmtSpeed, when, CAT_LABELS } from "../util";

export function Inspector({
  item,
  onPauseResume,
  onRemove,
  onVerifyHint,
  onClose,
}: {
  item: Item;
  onPauseResume: () => void;
  onRemove: () => void;
  onVerifyHint: () => void;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [tFiles, setTFiles] = useState<{ path: string; size: number }[] | null>(null);
  const dl = item.status === "downloading";
  const finished = item.status === "completed";
  const failed = item.status === "error";
  const pct = item.size ? Math.min(100, (item.downloaded / item.size) * 100) : 0;
  const ext = ((/\.([a-z0-9]{1,8})$/i.exec(item.filename) || [])[1]?.toUpperCase().slice(0, 5)) || "";

  // 种子任务的文件清单要按需探测(item 里只有选中索引,没有名字)。
  // 轮询每拍都会反序列化出新的 torrentFiles 对象,不能进依赖;按
  // 「任务 id + 链接」只探测一次,否则面板里的文件列表每 500ms
  // 清空重载一闪一闪,后端也跟着反复探测。
  const probedKey = useRef("");
  useEffect(() => {
    const key = `${item.id}\n${item.url}`;
    if (!item.torrentFiles || probedKey.current === key) return;
    probedKey.current = key;
    setTFiles(null);
    api
      .probeTorrent(item.url)
      .then((info) => setTFiles(info.files))
      .catch(() => {});
  }, [item.id, item.url, item.torrentFiles]);

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      /* 剪贴板可能被浏览器挡住 */
    }
  };

  const statusText = dl
    ? "下载中"
    : item.status === "queued"
      ? item.note || "排队中"
      : item.status === "paused"
        ? "已暂停"
        : finished
          ? "已完成"
          : item.error || "失败";
  const statusClass = failed
    ? "err-text"
    : dl
      ? "running-text"
      : item.status === "paused"
        ? "paused-text"
        : "";

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="detail-overlay" />
        <Dialog.Content className="download-detail" aria-describedby={undefined}>
          <header className="detail-heading">
            <div>
              <Dialog.Title asChild>
                <span>下载详情</span>
              </Dialog.Title>
              <small>{ext ? `${ext} 文件` : item.torrentFiles ? "BT 种子" : CAT_LABELS[item.category] || "文件"}</small>
            </div>
            <Dialog.Close asChild>
              <button aria-label="关闭下载详情" title="返回列表">
                <Icon n="x" size={19} />
              </button>
            </Dialog.Close>
          </header>

      <div className="detail-file">
        <FileKind name={item.filename} torrent={!!item.torrentFiles} category={item.category} />
        <strong title={item.filename}>{item.filename}</strong>
        <span className="type-badge">
          {item.torrentFiles ? `BT · ${item.torrentFiles.length} 文件` : ext || CAT_LABELS[item.category] || "文件"}
        </span>
      </div>

      <div className="detail-progress">
        <div>
          <span>下载进度</span>
          <b>{pct.toFixed(pct >= 100 || pct === 0 ? 0 : 1)}%</b>
        </div>
        <div className="detail-progress-track">
          <i
            className={item.status === "paused" ? "paused" : failed ? "err" : ""}
            style={{ width: `${pct}%` }}
          />
        </div>
        <p>
          {bytes(item.downloaded)} / {item.size ? bytes(item.size) : "大小未知"}
          {dl && item.speed > 0 ? ` · ${fmtSpeed(item.speed)}` : ""}
        </p>
      </div>

      {tFiles && tFiles.length > 0 && (
        <section className="torrent-files">
          <div className="torrent-files-title">
            <span>文件列表</span>
            <small>{item.torrentFiles!.length}/{tFiles.length} 个已选</small>
          </div>
          <div className="torrent-file-list">
            {tFiles.map((f, i) => {
              const picked = item.torrentFiles!.includes(i);
              return (
                <div className={`torrent-file-row${picked ? "" : " skipped"}`} key={i}>
                  <div className="torrent-file-name" title={f.path}>
                    <FileKind name={f.path} />
                    <span>{f.path}</span>
                  </div>
                  <div className="torrent-file-size">
                    <span>{bytes(f.size)}</span>
                    <b className={picked ? "" : "off"}>{picked ? "已选" : "未选"}</b>
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      )}

      <dl className="detail-meta">
        <div>
          <dt>状态</dt>
          <dd className={statusClass} title={item.error || undefined}>
            {statusText}
          </dd>
        </div>
        <div>
          <dt>下载节点</dt>
          <dd>{item.torrentFiles ? `${item.connections} 个连接` : `${item.connections} 条连接`}</dd>
        </div>
        <div>
          <dt>下载速度</dt>
          <dd>{dl && item.speed > 0 ? fmtSpeed(item.speed) : "—"}</dd>
        </div>
        <div>
          <dt>保存位置</dt>
          <dd title={item.path}>{item.path || "—"}</dd>
        </div>
        <div>
          <dt>来源</dt>
          <dd title={item.torrentFiles ? "BitTorrent" : item.url}>
            {item.torrentFiles
              ? "BitTorrent"
              : parseEd2k(item.url)
                ? "eDonkey 网络(电驴)"
                : host(item.url) || item.url}
            <button className="copy" title="复制链接" onClick={() => copy(item.url)}>
              <Icon n={copied ? "check" : "copy"} size={13} />
            </button>
          </dd>
        </div>
        <div>
          <dt>创建时间</dt>
          <dd>{when(item.addedAt) || "—"}</dd>
        </div>
      </dl>

      {finished && item.checksum && !item.verified && (
        <button className="verify-line" onClick={onVerifyHint} title="校验在下载完成时进行;重试可再校验一次">
          SHA-256 未校验——点此重新入队再校验一次
        </button>
      )}

      <div className={`detail-actions${finished ? " single-action" : ""}`}>
        {!finished && (
          <button className="detail-primary" onClick={onPauseResume}>
            <Icon n={dl || item.status === "queued" ? "pause" : "play"} size={16} />
            {dl || item.status === "queued" ? "暂停下载" : failed ? "重试下载" : "继续下载"}
          </button>
        )}
        <button
          className={finished ? "detail-primary danger" : "detail-secondary danger"}
          onClick={onRemove}
        >
          <Icon n="trash" size={15} />
          删除任务
        </button>
      </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
