// <dialog> 的 React 包装:open 变化时 showModal/close,Esc 关闭时回调。

import { useEffect, useRef, type ReactNode } from "react";

export function Dialog({
  open,
  onClose,
  className,
  children,
}: {
  open: boolean;
  onClose: () => void;
  className?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dlg = ref.current;
    if (!dlg) return;
    if (open && !dlg.open) dlg.showModal();
    if (!open && dlg.open) dlg.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      className={className}
      onClose={onClose}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      {children}
    </dialog>
  );
}
