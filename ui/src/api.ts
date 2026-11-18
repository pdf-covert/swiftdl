// 与 Go 后端的桥:所有命令走 POST api/invoke({cmd,args}),命令名与参数
// 形状沿用上游 Tauri 命令;推送换成 api/state 版本号轮询。

export type Status = "queued" | "downloading" | "paused" | "completed" | "error";
export type Header = [string, string];

export interface Item {
  id: number;
  url: string;
  filename: string;
  path: string;
  size: number | null;
  downloaded: number;
  status: Status;
  error: string | null;
  category: string;
  addedAt: number;
  completedAt: number | null;
  resumable: boolean;
  headers: Header[];
  remaining: [number, number][];
  /** 每连接的实时 (start, pos, end)。 */
  segments: [number, number, number][];
  speed: number;
  connections: number;
  note: string;
  torrentFiles: number[] | null;
  checksum?: string;
  mirrors?: string[];
  verified?: boolean;
}

export interface Settings {
  downloadDir: string;
  connections: number;
  maxConcurrent: number;
  speedLimitKib: number;
  sortIntoCategories: boolean;
  askBeforeDownload: boolean;
  onboardingDismissed: boolean;
  theme: string;
  seedUntil: string;
  startAt: number;
  proxy: string;
  uiScale: number;
}

export interface AppState {
  items: Item[];
  settings: Settings;
  pending: unknown[];
  version: string;
  v: number;
  /** 本地开发模式(SWIFTDL_DEV_FOLDERS):目录固定,不出选择弹窗。 */
  dev?: boolean;
}

export interface ProbeInfo {
  filename: string;
  size: number | null;
  resumable: boolean;
  category: string;
  dir: string;
}

export interface TorrentInfo {
  name: string;
  size: number;
  files: { path: string; size: number }[];
}

export interface AddRequest {
  url: string;
  filename: string;
  dir: string;
  headers?: Header[];
  start: boolean;
  size?: number | null;
  torrentFiles?: number[] | null;
  checksum?: string | null;
  mirrors?: string[];
  /** 重新下载:先删同路径旧任务与旧文件(经确认框确认) */
  overwrite?: boolean;
}

export interface DirEntry { name: string; path: string; writable: boolean }
export interface DirListing { path: string; parent: string; entries: DirEntry[] }
/** 网络诊断一行:ok=null 是纯信息行 */
export interface DiagRow { name: string; ok: boolean | null; detail: string }

// 拆开写:Vite 只静态改写 new URL(字面量, import.meta.url) 模式,
// 间接引用让它在运行时解析——开发下是 '/',fnOS 网关下是 '/app/swiftdl/'。
const here = import.meta.url;
const BASE = new URL("..", here).pathname;

async function post<T>(cmd: string, args: Record<string, unknown> = {}): Promise<T> {
  const resp = await fetch(`${BASE}api/invoke`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cmd, args }),
  });
  const j = await resp.json();
  if (!j.ok) throw new Error(j.error || "调用失败");
  return j.data as T;
}

export const api = {
  probeUrl: (url: string, headers: Header[] = []) => post<ProbeInfo>("probe_url", { url, headers }),
  probeTorrent: (url: string, headers: Header[] = []) => post<TorrentInfo>("probe_torrent", { url, headers }),
  saveTorrentFile: (name: string, content: string) => post<string>("save_torrent_file", { name, content }),
  isTorrentUrl: (url: string, filename: string | null = null) => post<boolean>("is_torrent_url", { url, filename }),
  /** 新建下载前的同名检查:目标目录里是否已有同名文件/文件夹 */
  pathExists: (dir: string, name: string) =>
    post<{ exists: boolean; path?: string; size?: number; isDir?: boolean }>("path_exists", { dir, name }),
  defaultDir: (filename: string) => post<string>("default_dir", { filename }),
  addDownload: (req: AddRequest) => post<number>("add_download", { req }),
  addLinks: (text: string) => post<number>("add_links", { text }),
  addMetalink: (url: string) => post<[string, number]>("add_metalink", { url }),
  grabPage: (url: string) => post<{ url: string; name: string }[]>("grab_page", { url }),
  pause: (ids: number[]) => post<void>("pause", { ids }),
  resume: (ids: number[]) => post<void>("resume", { ids }),
  remove: (ids: number[], deleteFiles: boolean) => post<void>("remove", { ids, deleteFiles }),
  setUrl: (id: number, url: string) => post<void>("set_url", { id, url }),
  downloadNext: (id: number) => post<boolean>("download_next", { id }),
  getItem: (id: number) => post<Item | null>("get_item", { id }),
  setSettings: (settings: Settings) => post<void>("set_settings", { settings }),
  listDirs: (path: string) => post<DirListing>("list_dirs", { path }),
  /** 用户在 fnOS 授权给本应用的文件夹(实时);error 非空 = 查询失败 */
  authorizedDirs: () => post<{ dirs: string[]; error: string }>("authorized_dirs", {}),
  /** BT 网络自检:DNS/DoH/DHT 引导/UDP tracker/TCP/限速代理 */
  netDiag: () => post<DiagRow[]>("net_diag", {}),
};

/** 带版本号的轮询:没变只回 {same,v}。 */
export async function fetchState(v: number): Promise<{ same: true; v: number } | (AppState & { same?: false })> {
  const resp = await fetch(`${BASE}api/state?v=${v}`);
  return resp.json();
}
