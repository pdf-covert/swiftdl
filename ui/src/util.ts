// 文件分类与数字格式化,上游 util.ts 的移植(图标挪去了 icons.tsx)。

export interface Cat { color: string; icon: string }
export const CATS: Record<string, Cat> = {
  Video: { color: "#8b5cf6", icon: "video" },
  Music: { color: "#ec4899", icon: "music" },
  Programs: { color: "#3b82f6", icon: "app" },
  Compressed: { color: "#f59e0b", icon: "archive" },
  Documents: { color: "#10b981", icon: "doc" },
  Images: { color: "#f97316", icon: "image" },
  Other: { color: "#8b8f98", icon: "file" },
};
export const catInfo = (name: string | null | undefined): Cat => CATS[name ?? ""] || CATS.Other;

export const CAT_LABELS: Record<string, string> = {
  Video: "视频",
  Music: "音乐",
  Programs: "程序",
  Compressed: "压缩包",
  Documents: "文档",
  Images: "图片",
  Other: "其他",
};

/** 文件名按扩展名归的类(与后端一致)。 */
export function catOf(name: string | null | undefined): string {
  const e = (/\.([a-z0-9]{1,5})$/i.exec(name || "") || [])[1]?.toLowerCase() || "";
  if (/^(zip|rar|7z|tar|gz|tgz|xz|bz2|zst|iso|img)$/.test(e)) return "Compressed";
  if (/^(pdf|docx?|xlsx?|pptx?|odt|txt|rtf|epub|csv|md)$/.test(e)) return "Documents";
  if (/^(mp3|wav|flac|aac|ogg|m4a|opus|wma)$/.test(e)) return "Music";
  if (/^(mp4|mkv|avi|mov|webm|flv|wmv|m4v|mpe?g|3gp|ts)$/.test(e)) return "Video";
  if (/^(exe|msi|dmg|pkg|deb|rpm|apk|appimage|msix|bat|jar)$/.test(e)) return "Programs";
  if (/^(jpe?g|png|gif|webp|svg|bmp|heic|avif)$/.test(e)) return "Images";
  return "Other";
}

const UNITS = ["B", "KB", "MB", "GB", "TB"];
/** 字节数拆成 [数值, 单位]。 */
export function parts(n: number | null | undefined): [string, string] {
  if (n == null || !isFinite(n)) return ["—", ""];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return [
    i === 0 ? String(Math.round(v)) : v < 10 ? v.toFixed(2) : v < 100 ? v.toFixed(1) : String(Math.round(v)),
    UNITS[i],
  ];
}
export const bytes = (n: number | null | undefined): string => (n == null ? "—" : parts(n).join(" "));
export const speed = (b: number): string => (b > 0 ? `${bytes(b)}/s` : "—");
export function dur(sec: number): string {
  if (!isFinite(sec) || sec <= 0) return "—";
  sec = Math.ceil(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return h ? `${h}小时${m}分` : m ? `${m}分${s}秒` : `${s}秒`;
}
/** 简短相对时间:"刚刚"、"5 分钟前"、"14:32"、"昨天"、"11月1日"。 */
export function when(ms: number | null | undefined): string {
  if (!ms) return "";
  const d = new Date(ms), now = new Date();
  const mins = Math.round((now.getTime() - d.getTime()) / 60000);
  if (mins < 1) return "刚刚";
  if (mins < 60) return `${mins} 分钟前`;
  if (d.toDateString() === now.toDateString())
    return d.toLocaleTimeString("zh-CN", { hour: "numeric", minute: "2-digit" });
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return "昨天";
  return d.toLocaleDateString("zh-CN", { month: "numeric", day: "numeric" });
}
export const host = (url: string): string => {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
};

export interface ED2KFile { name: string; size: number }

/**
 * 解析 ed2k://|file|文件名|大小|MD4|/ 链接(电驴)。协议头不区分大小写;
 * 大小必须是正整数,哈希是 32 位十六进制。不是文件链接返回 null。
 */
export function parseEd2k(url: string): ED2KFile | null {
  const u = url.trim();
  if (!/^ed2k:\/\//i.test(u)) return null;
  const parts = u.split("|");
  if (parts.length < 6 || parts[parts.length - 1] !== "/") return null;
  if (parts[1].toLowerCase() !== "file") return null;
  const size = Number(parts[3]);
  if (!Number.isInteger(size) || size <= 0) return null;
  if (!/^[0-9A-Fa-f]{32}$/.test(parts[4])) return null;
  let name = parts[2];
  try {
    name = decodeURIComponent(name);
  } catch {
    /* 有百分号但不是合法转义,按原样用 */
  }
  return { name, size };
}

/** 剩余时间,拿不到就是 —。 */
export function eta(item: { size: number | null; downloaded: number; speed: number }): string {
  if (!item.size || item.speed <= 0) return "—";
  return dur((item.size - item.downloaded) / item.speed);
}
