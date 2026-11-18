package com.mindfs.app;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

/**
 * 后台回复轮询的退避策略。
 *
 * <p>用户报的现象是「app 在后台时间一长就完全不再提醒」。线上 nginx 记录里
 * 每次断流后都跟着一段 10 分钟的静默 —— 那正是旧的固定 {@code WAIT_RETRY_INTERVAL_MS}
 * 毫秒数：连一次隧道抖动都要等满十分钟。这里钉住新的指数退避边界。</p>
 */
public class ReplyRetryPolicyTest {
    @Test
    public void retryDelayStartsSmallAndDoubles() {
        assertEquals(ReplyRetryPolicy.RETRY_BASE_MILLIS, ReplyRetryPolicy.retryDelayMillis(1));
        assertEquals(ReplyRetryPolicy.RETRY_BASE_MILLIS * 2, ReplyRetryPolicy.retryDelayMillis(2));
        assertEquals(ReplyRetryPolicy.RETRY_BASE_MILLIS * 4, ReplyRetryPolicy.retryDelayMillis(3));
    }

    @Test
    public void retryDelayIsCapped() {
        for (int attempt = 1; attempt <= 64; attempt++) {
            long delay = ReplyRetryPolicy.retryDelayMillis(attempt);
            assertTrue("attempt " + attempt + " delay must stay positive", delay > 0);
            assertTrue(
                "attempt " + attempt + " delay " + delay + " exceeds cap",
                delay <= ReplyRetryPolicy.RETRY_MAX_MILLIS
            );
        }
        assertEquals(ReplyRetryPolicy.RETRY_MAX_MILLIS, ReplyRetryPolicy.retryDelayMillis(32));
    }

    @Test
    public void retryDelayIsMonotonic() {
        long previous = 0;
        for (int attempt = 1; attempt <= 10; attempt++) {
            long delay = ReplyRetryPolicy.retryDelayMillis(attempt);
            assertTrue("delay must not shrink: " + previous + " -> " + delay, delay >= previous);
            previous = delay;
        }
    }

    @Test
    public void zeroAttemptFallsBackToBaseDelay() {
        assertEquals(ReplyRetryPolicy.RETRY_BASE_MILLIS, ReplyRetryPolicy.retryDelayMillis(0));
        assertEquals(ReplyRetryPolicy.RETRY_BASE_MILLIS, ReplyRetryPolicy.retryDelayMillis(-3));
    }

    /** 网络抖动一分钟内只允许退到 60s，绝不能再出现 10 分钟的空窗。 */
    @Test
    public void worstCaseRetryWindowIsOneMinute() {
        assertEquals(60_000L, ReplyRetryPolicy.RETRY_MAX_MILLIS);
        assertEquals(60_000L, ReplyRetryPolicy.retryDelayMillis(8));
    }
}
