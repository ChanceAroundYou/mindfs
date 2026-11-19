package com.mindfs.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.util.Log;
import androidx.core.content.ContextCompat;

/**
 * 兜底唤醒闹钟的接收端。
 *
 * <p>为什么是广播而不是 {@code PendingIntent.getService}：这个闹钟唯一的用途就是
 * 「进程已经被系统清掉之后把轮询拉回来」。Android 8+ 禁止后台进程直接 startService，
 * 老实现递的是 getService 的 PendingIntent —— 在唯一需要它的场景下必然起不来，
 * 于是后台时间一长就再无任何请求（实测 nginx 侧整段静默）。广播接收器没有这个限制：
 * 它可以申请前台服务，而且以 {@code setAlarmClock} 精确闹钟送达的广播本身就带
 * 「允许启动前台服务」的豁免（Android 12+ 的后台启动限制不拦它）；被拒时这里吞掉异常
 * 并继续排下一次。</p>
 *
 * <p>排闹钟这一步必须在接收器里做：进程被杀期间没有任何代码会重排，只有这里补得上。</p>
 */
public class ReplyPollerWatchdogReceiver extends BroadcastReceiver {
    private static final String TAG = "MindFSReplyPoller";

    @Override
    public void onReceive(Context context, Intent intent) {
        String action = intent == null ? "" : intent.getAction();
        if (!ReplyPollerService.ACTION_WATCHDOG.equals(action)) {
            return;
        }
        // 先排下一次：无论下面这次能不能把服务拉起来，3 分钟后都要再试一次。
        // 间隔常量从服务里取 —— 两处各写一个数字迟早会漂（15 分钟那个旧值就是漂出来的）。
        ReplyPollerService.scheduleWatchdogAt(context, ReplyPollerService.WATCHDOG_INTERVAL_MS);
        if (!ReplyPollerService.isConfigured(context)) {
            Log.i(TAG, "watchdog: reply poller is not configured yet, skipping");
            return;
        }
        Intent start = new Intent(context, ReplyPollerService.class)
            .setAction(ReplyPollerService.ACTION_WATCHDOG);
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                ContextCompat.startForegroundService(context, start);
            } else {
                context.startService(start);
            }
            Log.i(TAG, "watchdog: reply poller restarted");
        } catch (RuntimeException ex) {
            // Android 12+ 在后台拒绝启动前台服务（应用不在电池优化白名单里时最常见）。
            // 这不是崩溃理由：下一轮闹钟、开机广播、或者用户下次打开 App 都会再试。
            Log.w(TAG, "watchdog: failed to restart reply poller", ex);
        }
    }
}
