// 小幅速度曲线:canvas 上最近 N 拍的折线,自动适应最大值。

import { useEffect, useRef } from "react";

export function SpeedGraph({ history, color, id }: { history: number[]; color?: string; id?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const dpr = window.devicePixelRatio || 1;
    const w = cv.clientWidth, h = cv.clientHeight;
    if (cv.width !== w * dpr || cv.height !== h * dpr) {
      cv.width = w * dpr;
      cv.height = h * dpr;
    }
    const ctx = cv.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);
    const max = Math.max(1, ...history);
    ctx.beginPath();
    history.forEach((v, i) => {
      const x = (i / Math.max(1, history.length - 1)) * w;
      const y = h - 1 - (v / max) * (h - 2);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.strokeStyle = color || getComputedStyle(cv).color || "#007aff";
    ctx.lineWidth = 1.25;
    ctx.stroke();
  }, [history, color]);
  // 尺寸交给 CSS(按 id 定高),这里只负责画。
  return <canvas ref={ref} id={id} />;
}
