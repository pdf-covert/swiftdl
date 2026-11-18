// 服务器端目录浏览器:只列用户在 fnOS 授权给本应用的共享文件夹。
// 根视图 = 授权文件夹列表;没有授权时给指引横幅,去 fnOS 应用设置授权。

import { useCallback, useEffect, useState } from "react";
import { api, type DirListing } from "../api";
import { Icon } from "../icons";
import { Dialog } from "./Dialog";

export function FolderPicker({
  open,
  start,
  onPick,
  onClose,
}: {
  open: boolean;
  start: string | null;
  onPick: (path: string) => void;
  onClose: () => void;
}) {
  const [listing, setListing] = useState<DirListing | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [authErr, setAuthErr] = useState<string | null>(null);

  const load = useCallback((path: string) => {
    setErr(null);
    api
      .listDirs(path)
      .then(setListing)
      .catch((e) => setErr(String(e.message || e)));
  }, []);

  // 打开时:先查授权列表;有授权再看 start(原目录可能已被收回授权)。
  const checkAuth = useCallback(() => {
    setListing(null);
    setErr(null);
    setAuthErr(null);
    api
      .authorizedDirs()
      .then(({ dirs, error }) => {
        if (error) {
          setAuthErr(error);
          return;
        }
        if (!dirs.length) {
          setAuthErr("还没有把任何共享文件夹授权给 SwiftDL");
          return;
        }
        load(start || "");
      })
      .catch((e) => setAuthErr(String(e.message || e)));
  }, [load, start]);

  useEffect(() => {
    if (open) checkAuth();
  }, [open, checkAuth]);

  const atRoot = listing != null && listing.path === "";

  return (
    <Dialog open={open} onClose={onClose} className="modal picker">
      <div className="modal-heading">
        <h2>选择文件夹</h2>
        <button type="button" className="modal-close" onClick={onClose} aria-label="关闭弹窗" title="关闭 (Esc)">
          <Icon n="x" size={19} />
        </button>
      </div>

      {authErr ? (
        <div className="pick-guide">
          <span className="pg-ic">
            <Icon n="lock" size={22} />
          </span>
          <b>需要先授权文件夹</b>
          <p>下载只能保存到授权给 SwiftDL 的共享文件夹里:</p>
          <p className="pg-steps">fnOS 应用中心 → SwiftDL 下载 → 设置/访问权限 →「允许访问以下文件夹」里添加(管理员);或普通用户在自己文件上授权</p>
          <p>授权后点下方按钮即可,无需重启应用。</p>
          <button type="button" className="outline-button" onClick={checkAuth}>
            <Icon n="retry" size={15} />
            我已授权,重新查询
          </button>
          <p className="pick-err">{authErr}</p>
        </div>
      ) : (
        <>
          <div className="pick-crumbs">
            {!atRoot && (
              <button type="button" className="pick-back" onClick={() => load(listing?.parent || "")}>
                <Icon n="chevron" size={13} /> {listing?.parent ? "上一级" : "授权文件夹"}
              </button>
            )}
            <span className="pick-path">{atRoot ? "已授权的文件夹" : listing ? listing.path : "…"}</span>
          </div>
          <div className="pick-list">
            {err && (
              <div className="pick-err-row">
                <p className="pick-err">{err}</p>
                <button type="button" className="pick-back" onClick={() => load("")}>
                  回到授权文件夹
                </button>
              </div>
            )}
            {!err &&
              listing &&
              listing.entries.map((e) => (
                <button key={e.path} className="pick-row" onClick={() => load(e.path)} onDoubleClick={() => onPick(e.path)}>
                  <Icon n="folder" size={17} />
                  <span className="pick-name">{e.name}</span>
                  {!e.writable && <span className="pick-ro">只读</span>}
                  <Icon n="chevron" size={12} />
                </button>
              ))}
            {!err && listing && listing.entries.length === 0 && (
              <p className="pick-empty">{atRoot ? "没有可列的文件夹" : "这个文件夹里没有子文件夹,可用下方按钮选定当前层"}</p>
            )}
          </div>
          <div className="modal-footer">
            <button type="button" className="cancel-button" onClick={onClose}>
              取消
            </button>
            <button
              type="button"
              className="submit-button"
              disabled={!listing || listing.path === ""}
              onClick={() => listing && onPick(listing.path)}
              title={atRoot ? "进入某个文件夹后,或双击授权文件夹本身" : "选当前文件夹"}
            >
              选这个文件夹
            </button>
          </div>
        </>
      )}
    </Dialog>
  );
}
