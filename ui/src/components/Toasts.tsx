// 右下角提示:成功两秒半淡出,出错多留一会儿。

import { useEffect, useRef, useState } from "react";
import { Icon } from "../icons";

export interface Toast {
  id: number;
  text: string;
  kind: "ok" | "err";
}

let nextId = 1;
export function pushToast(list: Toast[], text: string, kind: "ok" | "err" = "ok"): Toast[] {
  return [...list.slice(-4), { id: nextId++, text, kind }];
}

export function useToasts() {
  const [list, setList] = useState<Toast[]>([]);
  const toast = (text: string, kind: "ok" | "err" = "ok") =>
    setList((l) => (l.some((t) => t.text === text) ? l : pushToast(l, text, kind)));
  const gone = (id: number) => setList((l) => l.filter((t) => t.id !== id));
  return { list, toast, gone };
}

export function Toasts({ list, onGone }: { list: Toast[]; onGone: (id: number) => void }) {
  const scheduled = useRef<Set<number>>(new Set());
  useEffect(() => {
    // 每条只排一次闹钟;列表变化不重置老提示的倒计时。
    for (const t of list) {
      if (scheduled.current.has(t.id)) continue;
      scheduled.current.add(t.id);
      setTimeout(() => onGone(t.id), t.kind === "err" ? 5000 : 2500);
    }
  }, [list, onGone]);
  return (
    <div className="toasts">
      {list.map((t) => (
        <div key={t.id} className="toast">
          <span className={`t-ic ${t.kind}`}>
            <Icon n={t.kind === "err" ? "alert" : "check"} size={13} />
          </span>
          <span>{t.text}</span>
        </div>
      ))}
    </div>
  );
}
