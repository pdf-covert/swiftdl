// 图标:设计稿 Icon 的 path 表配成 React 组件;FileKind 是设计稿的
// 文件类型色块(圆角块 + 扩展名文字,BT 种子给 BT)。

import { catOf } from "./util";

const P: Record<string, string> = {
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.2-4.2"/>',
  compose:
    '<path d="M12 4H6.5A2.5 2.5 0 0 0 4 6.5v11A2.5 2.5 0 0 0 6.5 20h11a2.5 2.5 0 0 0 2.5-2.5V12"/><path d="M17.6 3.6a2 2 0 0 1 2.8 2.8L12.5 14.3 9 15l.7-3.5z"/>',
  settings:
    '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  download: '<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>',
  pause: '<path d="M9 5v14M15 5v14"/>',
  play: '<path d="m8 5 11 7-11 7V5Z"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  globe: '<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5a13 13 0 0 1 0 17M12 3.5a13 13 0 0 0 0 17"/>',
  chevron: '<path d="m9.5 6 6 6-6 6"/>',
  menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
  chevDown: '<path d="m6 9.5 6 6 6-6"/>',
  chevUp: '<path d="m6 14.5 6-6 6 6"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  folder: '<path d="M3.5 7.5A2 2 0 0 1 5.5 5.5H9l2 2h7.5a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/>',
  lock: '<rect x="5.5" y="10.5" width="13" height="9" rx="2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>',
  open: '<path d="M14 4.5h5.5V10M19.5 4.5 11 13M18 14v4a1.5 1.5 0 0 1-1.5 1.5h-10A1.5 1.5 0 0 1 5 18V8a1.5 1.5 0 0 1 1.5-1.5h4"/>',
  copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5V6a1.5 1.5 0 0 0-1.5-1.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
  retry: '<path d="M4.5 12a7.5 7.5 0 0 1 13.2-4.8L19.5 9M19.5 4.5V9H15M19.5 12a7.5 7.5 0 0 1-13.2 4.8L4.5 15M4.5 19.5V15H9"/>',
  trash: '<path d="M4.5 7h15M10 11v6M14 11v6M6.5 7l.8 11.2A2 2 0 0 0 9.3 20h5.4a2 2 0 0 0 2-1.8L17.5 7M9.5 7V5a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1v2"/>',
  more: '<circle cx="5" cy="12" r="1.4"/><circle cx="12" cy="12" r="1.4"/><circle cx="19" cy="12" r="1.4"/>',
  gauge: '<path d="M5 16a7.5 7.5 0 1 1 14 0"/><path d="m12 12 4-3M5 19h14"/>',
  info: '<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5M12 8h.01"/>',
  clipboard: '<rect x="6" y="4.5" width="12" height="16" rx="2"/><path d="M9.5 4.5h5v2.5h-5z"/>',
  check: '<path d="m5 12 4.2 4L19 6.5"/>',
  doneCircle: '<circle cx="12" cy="12" r="8.5"/><path d="m8.5 12 2.5 2.5 4.5-5"/>',
  alert: '<circle cx="12" cy="12" r="8.5"/><path d="M12 8v5M12 16h.01"/>',
  bolt: '<path d="M13 3.5 5.5 13.5H11l-1 7 7.5-10H12z"/>',
  hash: '<path d="M5 9h14M5 15h14M10 4 8 20M16 4l-2 16"/>',
  arrowUp: '<path d="M12 19V5M6.5 10.5 12 5l5.5 5.5"/>',
  video: '<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="M7.5 4.5v15M16.5 4.5v15M3.5 9.5h4M3.5 14.5h4M16.5 9.5h4M16.5 14.5h4"/>',
  music: '<path d="M9 17.5V6.3l10-1.8v11"/><circle cx="6.5" cy="17.5" r="2.5"/><circle cx="16.5" cy="15.5" r="2.5"/>',
  app: '<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="M3.5 9h17"/><path d="M6.6 6.8h.01M9 6.8h.01" stroke-width="2.2"/>',
  archive: '<path d="M12 3.3 20 7.4v9.2l-8 4.1-8-4.1V7.4z"/><path d="m4 7.4 8 4.1 8-4.1M12 11.5v9.2M8 5.3l8 4.1"/>',
  doc: '<path d="M7 3h7l4 4v14H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z"/><path d="M14 3v5h5"/>',
  image: '<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><circle cx="9" cy="10" r="1.7"/><path d="m20.5 15.5-4.5-4.5-9.5 8.5"/>',
  file: '<path d="M14 3.5H7.5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2V8z"/><path d="M14 3.5V8h4.5"/>',
};

export function Icon({ n, size = 20 }: { n: string; size?: number }) {
  return (
    <svg
      className="ic"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      width={size}
      height={size}
      dangerouslySetInnerHTML={{ __html: P[n] || P.file }}
    />
  );
}

/* 设计稿的七种类型色(背景/前景),分类映射到设计稿的桶。 */
const KIND_COLORS: Record<string, [string, string]> = {
  torrent: ["#ffcc66", "#ad7620"],
  media: ["#e5f2ff", "#397dc9"],
  document: ["#e7f0ff", "#4e7ecc"],
  application: ["#e5f6ed", "#32865c"],
  archive: ["#fff0db", "#bd7a24"],
  image: ["#eeeaff", "#7664c9"],
  generic: ["#eef1f5", "#7e8794"],
};
const CAT_KIND: Record<string, string> = {
  Video: "media",
  Music: "media",
  Programs: "application",
  Compressed: "archive",
  Documents: "document",
  Images: "image",
  Other: "generic",
};

/** 列表行的文件类型块:显示真实扩展名(最多 5 字符),种子任务给 BT。 */
export function FileKind({
  name,
  torrent,
  category,
}: {
  name: string;
  torrent?: boolean;
  category?: string;
}) {
  const e = torrent
    ? "BT"
    : ((/\.([a-z0-9]{1,8})$/i.exec(name) || [])[1]?.toUpperCase().slice(0, 5)) || "";
  const kind = torrent ? "torrent" : CAT_KIND[category || catOf(name)] || "generic";
  const [bg, fg] = KIND_COLORS[kind];
  return (
    <div className={`file-kind ${kind}`} style={{ background: bg, color: fg }} title={`${e || "文件"} 文件`}>
      {e || <Icon n="file" size={17} />}
    </div>
  );
}
