// 轮询钩子:500ms 一拍问版本号,没变不传数据;页面在后台放慢到 2s。

import { useEffect, useRef, useState } from "react";
import { fetchState, type AppState } from "./api";

export function useAppState(): AppState | null {
  const [state, setState] = useState<AppState | null>(null);
  const v = useRef(0);
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const j = await fetchState(v.current);
        if (!j.same) {
          v.current = j.v;
          setState(j as AppState);
        }
      } catch {
        /* 后端可能正好在保存;下一拍再试 */
      }
      if (!stopped) timer = setTimeout(tick, document.hidden ? 2000 : 500);
    };
    tick();
    const onVis = () => {
      clearTimeout(timer);
      tick();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      stopped = true;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, []);
  return state;
}
