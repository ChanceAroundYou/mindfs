// 控制面状态的唯一轮询入口。
//
// 常驻状态已从页面顶部的固定横幅收进左侧三点菜单；一次性「主控离线 / 恢复」
// 提示另由 ControlModeNotices 渲染。两处必须共用同一次轮询，否则会出现
// 「菜单说在同步、提示说离线」的分裂（旧 ControlModeBanner 的注释在说这件事）。
import React, { createContext, useContext } from "react";
import { useControlSyncStatus } from "../hooks/useControlSyncStatus";
import { ControlPairingGate } from "../components/app/ControlPairingGate";
import { ControlModeNotices } from "../components/app/ControlModeNotices";

export type ControlSyncValue = ReturnType<typeof useControlSyncStatus>;

const ControlSyncContext = createContext<ControlSyncValue | null>(null);

/** 菜单等消费方用这个读状态；不在 provider 内时返回 null，由调用方决定不渲染。 */
export function useControlSync(): ControlSyncValue | null {
  return useContext(ControlSyncContext);
}

export function ControlSyncProvider({ children }: { children: React.ReactNode }) {
  const value = useControlSyncStatus();
  return (
    <ControlSyncContext.Provider value={value}>
      {children}
      {value.pairing?.pending_pairing ? (
        // 未配对的边缘：状态横幅此刻没有意义，要出的是配对入口（与旧行为一致）。
        <ControlPairingGate
          pairing={value.pairing}
          open={value.pairingOpen}
          busy={value.pairingBusy}
          failure={value.pairingFailure}
          paired={value.paired}
          onOpen={value.openPairing}
          onClose={value.closePairing}
          onSubmit={value.submitPairing}
        />
      ) : (
        <ControlModeNotices control={value} />
      )}
    </ControlSyncContext.Provider>
  );
}
