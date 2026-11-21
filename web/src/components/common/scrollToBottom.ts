// 「回到底部」的共享几何与收敛逻辑。
//
// 会话时间线（G-KB/G-KC/G-KD 的回归修复）与任务详情面板共用这一份：按钮显隐只看
// 「离底部还有多远」，点击后逐帧写 maxTop，直到连续几帧没有位移。任务详情不再另写
// 一套阈值或动画，否则同一个按钮在两处会有两种手感。

/** 距离底部不超过该像素即视为「在底部」：按钮显隐与收敛判定共用。 */
export const STICK_REARM_PX = 2;

// 收敛节奏：新挂载行的实测（ResizeObserver）往往比写 scrollTop 晚一两帧落地，
// 只按「连续稳定」判定会在测量到达之前收工，停在估算高度算出来的位置。
export const SCROLL_CONVERGE_MIN_FRAMES = 8;
export const SCROLL_CONVERGE_STABLE_FRAMES = 3;
export const SCROLL_CONVERGE_MAX_FRAMES = 120;

/** 容器底部还剩多少像素没滚到（已到底时为 0）。 */
export function distanceFromBottom(el: HTMLElement): number {
  return Math.max(0, el.scrollHeight - el.clientHeight - el.scrollTop);
}

export type ConvergeScrollOptions = {
  /** 每帧调用；返回 false 立即让位（用户上滚 / 取消）。 */
  shouldContinue?: () => boolean;
};

/**
 * 逐帧收敛到容器底部，返回取消函数（幂等，可重复调用）。
 *
 * 不用 `scrollTo({ behavior: "smooth" })`：虚拟化或异步布局里每帧都可能有新行把
 * scrollHeight 撑大，一次即时写入就取消浏览器进行中的平滑动画，按钮会停在半路。
 */
export function convergeScrollToBottom(
  el: HTMLElement,
  options: ConvergeScrollOptions = {},
): () => void {
  let handle: number | null = null;
  let frames = 0;
  let stableFrames = 0;
  let cancelled = false;

  const stop = () => {
    cancelled = true;
    if (handle !== null) {
      window.cancelAnimationFrame(handle);
      handle = null;
    }
  };

  const step = () => {
    handle = null;
    if (cancelled) return;
    if (options.shouldContinue && !options.shouldContinue()) {
      stop();
      return;
    }
    const maxTop = Math.max(0, el.scrollHeight - el.clientHeight);
    if (maxTop - el.scrollTop > 1) {
      el.scrollTop = maxTop;
      stableFrames = 0;
    } else {
      stableFrames += 1;
    }
    frames += 1;
    if (
      (frames >= SCROLL_CONVERGE_MIN_FRAMES && stableFrames >= SCROLL_CONVERGE_STABLE_FRAMES) ||
      frames >= SCROLL_CONVERGE_MAX_FRAMES
    ) {
      stop();
      return;
    }
    handle = window.requestAnimationFrame(step);
  };

  handle = window.requestAnimationFrame(step);
  return stop;
}
