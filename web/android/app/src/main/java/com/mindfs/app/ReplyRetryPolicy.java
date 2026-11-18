package com.mindfs.app;

/**
 * 后台回复轮询的退避与重启策略（纯逻辑，JVM 单测覆盖）。
 *
 * <p>抽出来的原因有两个：策略要能在 JVM 上测；而它以前写死在 {@link ReplyPollerService}
 * 的循环里，三处「失败 → 睡 10 分钟」是同一个常量 {@code WAIT_RETRY_INTERVAL_MS}，
 * 于是「服务端重启了一次」「隧道断了几秒」都会把长轮询停掉十分钟 —— 后台提醒
 * 长时间不响的一大半来自这里。</p>
 */
final class ReplyRetryPolicy {
    /** 长轮询失败后第一次重试的等待；每次失败翻倍。 */
    static final long RETRY_BASE_MILLIS = 15_000L;
    /** 翻倍上限：一分钟一次，最坏情况也只是晚一分钟。 */
    static final long RETRY_MAX_MILLIS = 60_000L;
    /** 服务端明确说「没有这个端点」（404/405）时才用长等待 —— 那是能力问题，不是网络抖动。 */
    static final long ENDPOINT_MISSING_MILLIS = 600_000L;
    /** 普通轮询降级间隔。 */
    static final long POLL_INTERVAL_MILLIS = 10_000L;
    /** 单次请求失败后的短退避。 */
    static final long ERROR_BACKOFF_MILLIS = 5_000L;

    private ReplyRetryPolicy() {}

    /**
     * 第 {@code attempt} 次连续失败后该等多久（attempt 从 1 起）。
     * 15s → 30s → 60s → 60s…，不再有「一觉睡十分钟」。
     */
    static long retryDelayMillis(int attempt) {
        if (attempt <= 0) {
            return RETRY_BASE_MILLIS;
        }
        long delay = RETRY_BASE_MILLIS;
        for (int i = 1; i < attempt; i++) {
            delay *= 2;
            if (delay >= RETRY_MAX_MILLIS) {
                return RETRY_MAX_MILLIS;
            }
        }
        return delay;
    }

    /** 只剩普通轮询时的下一次间隔。 */
    static long pollIntervalMillis() {
        return POLL_INTERVAL_MILLIS;
    }
}
