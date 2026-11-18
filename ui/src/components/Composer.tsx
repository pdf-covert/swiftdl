// 新建下载:设计稿 create-modal——多行链接输入(聚焦才显示「从剪贴板
// 粘贴」)、种子拖放区、探测出的文件信息、下载目录、更多选项。
// 探测/批量/Metalink 逻辑沿用。

import { useEffect, useRef, useState } from "react";
import { api, type Header, type ProbeInfo, type TorrentInfo } from "../api";
import { FileKind, Icon } from "../icons";
import { bytes, catOf, CATS, host, parseEd2k } from "../util";
import { Dialog } from "./Dialog";
import { FolderPicker } from "./FolderPicker";

type Mode =
  | { kind: "idle" }
  | { kind: "probing" }
  | { kind: "file"; probe: ProbeInfo }
  | { kind: "torrent"; info: TorrentInfo; picked: Set<number>; path: string; file: string }
  | { kind: "batch"; links: string[] }
  | { kind: "metalink" }
  | { kind: "err"; message: string };

/** 种子文件默认勾选:有视频就只勾视频(常见的「只要正片」场景),否则全勾。 */
const defaultPicked = (files: TorrentInfo["files"]): Set<number> => {
  const vids = files.map((f, i) => (catOf(f.path) === "Video" ? i : -1)).filter((i) => i >= 0);
  return new Set(vids.length ? vids : files.map((_, i) => i));
};

/** 按后缀名给文件索引分组(显示真实文件类型),按总大小降序;all = 该后缀是否已全部勾选。 */
function torrentTypeGroups(info: TorrentInfo, picked: Set<number>) {
  const groups = new Map<string, number[]>();
  info.files.forEach((f, i) => {
    const e = (/\.([a-z0-9]{1,8})$/i.exec(f.path) || [])[1]?.toLowerCase() || "无后缀";
    const list = groups.get(e) ?? [];
    list.push(i);
    groups.set(e, list);
  });
  const total = (idx: number[]) => idx.reduce((s, i) => s + (info.files[i].size || 0), 0);
  return [...groups.entries()]
    .map(([ext, idx]) => ({
      ext,
      idx,
      color: CATS[catOf(ext === "无后缀" ? "" : `x.${ext}`)].color,
      all: idx.every((i) => picked.has(i)),
    }))
    .sort((a, b) => total(b.idx) - total(a.idx) || a.ext.localeCompare(b.ext));
}

const isMetalinkUrl = (u: string) => /^[^?#\s]+\.meta4($|[?#])|^[^?#\s]+\.metalink($|[?#])/i.test(u);

export function Composer({
  open,
  settings,
  initialUrl,
  dev,
  onClose,
  onAdded,
  onError,
}: {
  open: boolean;
  settings: { downloadDir: string; sortIntoCategories: boolean } | null;
  initialUrl?: string;
  /** 本地开发模式:下载目录固定,不显示「浏览」按钮。 */
  dev?: boolean;
  onClose: () => void;
  onAdded: (msg: string) => void;
  onError: (msg: string) => void;
}) {
  const [url, setUrl] = useState("");
  const [mode, setMode] = useState<Mode>({ kind: "idle" });
  const [filename, setFilename] = useState("");
  const [dir, setDir] = useState("");
  const [checksum, setChecksum] = useState("");
  const [mirrors, setMirrors] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [startNow, setStartNow] = useState(true);
  const [picking, setPicking] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [dup, setDup] = useState<{ path: string; size: number; isDir: boolean } | null>(null);
  const [dragOn, setDragOn] = useState(false);
  const probeSeq = useRef(0);

  // 上传本地 .torrent:base64 存到服务器,拿到路径后走普通种子探测/提交流程。
  const handleTorrentFile = async (file: File) => {
    setMode({ kind: "probing" });
    try {
      const data = new Uint8Array(await file.arrayBuffer());
      let bin = "";
      for (let i = 0; i < data.length; i += 0x8000)
        bin += String.fromCharCode(...data.subarray(i, i + 0x8000));
      const path = await api.saveTorrentFile(file.name, btoa(bin));
      const info = await api.probeTorrent(path);
      setMode({ kind: "torrent", info, picked: defaultPicked(info.files), path, file: file.name });
      setFilename(info.name);
    } catch (e) {
      setMode({ kind: "err", message: e instanceof Error ? e.message : String(e) });
    }
  };

  useEffect(() => {
    if (!open) return;
    setUrl(initialUrl || "");
    setMode({ kind: "idle" });
    setFilename("");
    setChecksum("");
    setMirrors("");
    setAdvanced(false);
    setStartNow(true);
    setDir(settings?.downloadDir || "");
  }, [open, initialUrl, settings?.downloadDir]);

  // 粘贴/输入后探测(去抖 500ms)。
  useEffect(() => {
    if (!open) return;
    const u = url.trim();
    const seq = ++probeSeq.current;
    if (!u) {
      setMode({ kind: "idle" });
      return;
    }
    const links = u.split(/\s+/).filter((s) => /^https?:\/\//i.test(s) || /^ed2k:\/\//i.test(s));
    if (links.length > 1) {
      setMode({ kind: "batch", links });
      return;
    }
    // ed2k 链接不用发探测请求:文件名和大小链接里就有。
    const ed2k = parseEd2k(u);
    if (ed2k) {
      setMode({
        kind: "file",
        probe: { filename: ed2k.name, size: ed2k.size, resumable: true, category: catOf(ed2k.name), dir: "" },
      });
      setFilename(ed2k.name);
      if (settings?.sortIntoCategories) {
        api
          .defaultDir(ed2k.name)
          .then((d) => setDir(d))
          .catch(() => {});
      }
      return;
    }
    const timer = setTimeout(async () => {
      try {
        if (isMetalinkUrl(u)) {
          setMode({ kind: "metalink" });
          return;
        }
        const torrent = await api.isTorrentUrl(u);
        if (seq !== probeSeq.current) return;
        if (torrent) {
          setMode({ kind: "probing" });
          const info = await api.probeTorrent(u);
          if (seq !== probeSeq.current) return;
          setMode({ kind: "torrent", info, picked: defaultPicked(info.files), path: u, file: "" });
          setFilename(info.name);
          return;
        }
        setMode({ kind: "probing" });
        const probe = await api.probeUrl(u);
        if (seq !== probeSeq.current) return;
        setMode({ kind: "file", probe });
        setFilename(probe.filename);
        if (settings?.sortIntoCategories && probe.dir) setDir(probe.dir);
      } catch (e) {
        if (seq !== probeSeq.current) return;
        setMode({ kind: "err", message: e instanceof Error ? e.message : String(e) });
      }
    }, 500);
    return () => clearTimeout(timer);
  }, [url, open, settings?.sortIntoCategories]);

  const pasteLink = async () => {
    try {
      const text = await navigator.clipboard.readText();
      if (text.trim()) setUrl(text);
    } catch {
      /* 浏览器可能拦剪贴板;输入框还在 */
    }
  };

  const submit = async (e?: React.FormEvent, confirmed = false) => {
    e?.preventDefault();
    const u = mode.kind === "torrent" ? mode.path : url.trim();
    if (!u) return;
    setSubmitting(true);
    try {
      // 目标目录里已有同名文件/文件夹时先问一句(确认后本次会话不再拦)。
      if (!confirmed && (mode.kind === "file" || mode.kind === "torrent")) {
        const destName =
          mode.kind === "torrent" ? filename || mode.info.name : filename || "download.bin";
        const ex = await api.pathExists(effDir, destName).catch(() => null);
        if (ex?.exists) {
          setDup({ path: ex.path || "", size: ex.size || 0, isDir: !!ex.isDir });
          return;
        }
      }
      if (mode.kind === "batch") {
        const n = await api.addLinks(u);
        onAdded(`已添加 ${n} 个下载`);
      } else if (mode.kind === "torrent") {
        const files = [...mode.picked].sort((a, b) => a - b);
        await api.addDownload({
          url: u,
          filename: filename || mode.info.name,
          dir: dir || settings?.downloadDir || "",
          start: startNow,
          torrentFiles: files,
          overwrite: confirmed || undefined,
        });
        onAdded(`已添加 ${filename || mode.info.name}`);
      } else {
        await api.addDownload({
          url: u,
          filename: filename || "download.bin",
          dir: dir || settings?.downloadDir || "",
          start: startNow,
          checksum: checksum.trim() || null,
          overwrite: confirmed || undefined,
          mirrors: mirrors
            .split(/\s+/)
            .map((s) => s.trim())
            .filter((s) => /^https?:\/\//i.test(s)),
        });
        onAdded(`已添加 ${filename || u}`);
      }
      onClose();
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  const headers: Header[] = []; // 预留:带 Cookie 探测
  void headers;
  const hasTorrentFile = mode.kind === "torrent" && !!mode.file;
  // 电驴:文件名由链接里的哈希钉死,不能改名;镜像/校验和也不适用。
  const isEd2k = !!parseEd2k(url.trim());
  const effDir = dir || settings?.downloadDir || "";

  return (
    <>
      <Dialog open={open} onClose={onClose} className="modal composer">
        <form
          onSubmit={submit}
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            const f = Array.from(e.dataTransfer.files).find((f) => /\.torrent$/i.test(f.name));
            if (f) {
              e.preventDefault();
              handleTorrentFile(f);
            }
          }}
        >
          <div className="modal-heading">
            <h2>新建下载</h2>
            <button type="button" className="modal-close" onClick={onClose} aria-label="关闭弹窗" title="关闭 (Esc)">
              <Icon n="x" size={19} />
            </button>
          </div>

          <div className="new-task-body">
            <textarea
              autoFocus
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="粘贴链接、磁力链接、ed2k 或一次粘贴多个..."
              aria-label="下载链接"
              spellCheck={false}
            />
            <button type="button" className="paste-link" onClick={pasteLink}>
              从剪贴板粘贴
            </button>
            <label
              className={`torrent-dropzone${hasTorrentFile ? " has-file" : ""}${dragOn ? " drag" : ""}`}
              onDragOver={(e) => {
                e.preventDefault();
                setDragOn(true);
              }}
              onDragLeave={(e) => {
                if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragOn(false);
              }}
              onDrop={(e) => {
                e.preventDefault();
                e.stopPropagation();
                setDragOn(false);
                const f = Array.from(e.dataTransfer.files).find((f) => /\.torrent$/i.test(f.name));
                if (f) handleTorrentFile(f);
              }}
            >
              <input
                type="file"
                accept=".torrent,application/x-bittorrent"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) handleTorrentFile(f);
                  e.target.value = "";
                }}
              />
              <Icon n="doc" size={26} />
              <b>{hasTorrentFile ? (mode as { file: string }).file : "选择 .torrent 种子文件"}</b>
              <span>{hasTorrentFile ? "已选择种子文件,点击可重新选择" : "或把种子拖到这里、拖进窗口任意位置"}</span>
            </label>

            {mode.kind === "file" && (
              <div className="detail-file">
                <FileKind name={filename || mode.probe.filename} category={mode.probe.category} />
                <input
                  className="detail-name"
                  value={filename}
                  onChange={(e) => setFilename(e.target.value)}
                  readOnly={isEd2k}
                  spellCheck={false}
                  aria-label="文件名"
                  title={filename}
                />
                <span className="cf-sub">
                  {mode.probe.size ? bytes(mode.probe.size) : "大小未知"} ·{" "}
                  {isEd2k
                    ? "eDonkey 网络(电驴)"
                    : `${mode.probe.resumable ? "支持断点续传" : "不支持续传(单流)"} · ${host(url) || mode.probe.category}`}
                </span>
              </div>
            )}

            {mode.kind === "torrent" && (
              <>
                <div className="detail-file">
                  <FileKind torrent name={mode.info.name} />
                  <div className="cf-name" title={mode.info.name}>
                    {mode.info.name}
                  </div>
                  <span className="cf-sub">
                    {mode.file ? `种子文件 ${mode.file} · ` : ""}
                    {bytes(mode.info.size)} · {mode.info.files.length} 个文件 · 已选 {mode.picked.size} 个
                  </span>
                </div>
                <div className="tfile-types">
                  {torrentTypeGroups(mode.info, mode.picked).map((g) => (
                    <button
                      key={g.ext}
                      type="button"
                      className={`ttype${g.all ? " on" : ""}`}
                      onClick={() => {
                        const next = new Set(mode.picked);
                        g.idx.forEach((i) => (g.all ? next.delete(i) : next.add(i)));
                        setMode({ ...mode, picked: next });
                      }}
                    >
                      <i style={{ background: g.color }} />
                      {g.ext}
                      <span>{g.idx.length}</span>
                    </button>
                  ))}
                </div>
                <div className="tfile-list">
                  {mode.info.files.map((f, i) => (
                    <label key={i} className="grab-row">
                      <input
                        type="checkbox"
                        checked={mode.picked.has(i)}
                        onChange={(e) => {
                          const next = new Set(mode.picked);
                          if (e.target.checked) next.add(i);
                          else next.delete(i);
                          setMode({ ...mode, picked: next });
                        }}
                      />
                      <span className="gn" title={f.path}>
                        {f.path}
                      </span>
                      <span className="gh">{bytes(f.size)}</span>
                    </label>
                  ))}
                </div>
              </>
            )}

            {mode.kind === "batch" && (
              <div className="new-task-note">
                <Icon n="download" size={14} />
                识别到 {mode.links.length} 个链接(含 [01-50] 编号模式会自动展开)
              </div>
            )}
            {mode.kind === "metalink" && (
              <div className="new-task-note">
                <Icon n="bolt" size={14} />
                Metalink 文件:镜像与校验和按里面的来
              </div>
            )}
            {mode.kind === "probing" && (
              <div className="new-task-note">
                <span className="spinner" />
                正在读取文件信息…
              </div>
            )}
            {mode.kind === "err" && (
              <div className="new-task-note err">
                <Icon n="alert" size={14} />
                {mode.message}
              </div>
            )}

            {(mode.kind === "file" || mode.kind === "torrent") && (
              <>
                <div className="form-field">
                  <span>
                    下载目录
                    <i>只能保存到 fnOS 已授权的共享文件夹</i>
                  </span>
                  <div className="path-field">
                    <Icon n="folder" size={16} />
                    <input readOnly value={effDir} placeholder="还没选下载目录" title={effDir} />
                    {!dev && (
                      <button type="button" onClick={() => setPicking(true)}>
                        浏览
                      </button>
                    )}
                  </div>
                </div>
                {mode.kind === "file" && !isEd2k && (
                  <>
                    <button type="button" className="adv-toggle" onClick={() => setAdvanced(!advanced)}>
                      {advanced ? "收起更多选项" : "更多选项"}
                    </button>
                    {advanced && (
                      <div className="comp-adv">
                        <label>
                          SHA-256 校验和(可选)
                          <input
                            className="txt"
                            value={checksum}
                            onChange={(e) => setChecksum(e.target.value)}
                            placeholder="下载完成后核对"
                            spellCheck={false}
                          />
                        </label>
                        <label>
                          镜像地址(可选,每行一个)
                          <textarea
                            className="txt"
                            rows={3}
                            value={mirrors}
                            onChange={(e) => setMirrors(e.target.value)}
                            placeholder={"https://mirror1.example.com/file.bin\nhttps://mirror2.example.com/file.bin"}
                            spellCheck={false}
                          />
                        </label>
                      </div>
                    )}
                  </>
                )}
              </>
            )}
          </div>

          <div className="modal-footer between">
            <label className="start-now">
              <input
                type="checkbox"
                checked={startNow}
                onChange={(e) => setStartNow(e.target.checked)}
              />
              <span>
                <Icon n="check" size={11} />
              </span>
              创建后立即开始
            </label>
            <div className="modal-actions">
              <button type="button" className="cancel-button" onClick={onClose}>
                取消
              </button>
              <button
                type="submit"
                className="submit-button"
                disabled={
                  submitting ||
                  !(mode.kind === "torrent" ? mode.path : url.trim()) ||
                  mode.kind === "idle" ||
                  mode.kind === "probing" ||
                  mode.kind === "err" ||
                  (mode.kind === "torrent" && mode.picked.size === 0) ||
                  // 没有下载目录(自己选的或设置里的)不能提交
                  (!dir && !settings?.downloadDir)
                }
              >
                {submitting ? "添加中…" : "下载"} <span>↵</span>
              </button>
            </div>
          </div>
        </form>
      </Dialog>
      <FolderPicker
        open={picking}
        start={dir || null}
        onPick={(p) => {
          setDir(p);
          setPicking(false);
        }}
        onClose={() => setPicking(false)}
      />

      <Dialog open={!!dup} onClose={() => setDup(null)} className="modal confirm">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setDup(null);
            submit(e, true);
          }}
        >
          <h3>已存在同名{dup?.isDir ? "文件夹" : "文件"}</h3>
          <p>
            {dup?.isDir
              ? `目标目录里已有同名文件夹${dup && dup.size > 0 ? `(${bytes(dup.size)})` : ""}。重新下载会先删除原任务和已下载的文件,再从头下载。`
              : `目标目录里已有同名文件${dup && dup.size > 0 ? `(${bytes(dup.size)})` : ""}。重新下载会先删除原任务和已下载的文件,再从头下载。`}
            {dup?.path && (
              <small className="dup-path" title={dup.path}>
                {dup.path}
              </small>
            )}
          </p>
          <div className="delete-dialog-actions">
            <button type="button" onClick={() => setDup(null)}>
              取消
            </button>
            <button type="submit" className="delete-confirm">
              重新下载
            </button>
          </div>
        </form>
      </Dialog>
    </>
  );
}
