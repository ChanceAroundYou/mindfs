package com.mindfs.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.util.Log;
import androidx.core.content.ContextCompat;

/**
 * 开机 / 装完更新之后把常驻回复轮询拉起来。
 *
 * <p>系统的 START_STICKY 只在「同一个开机周期内进程被杀」时生效，跨重启就没了；
 * 而 MY_PACKAGE_REPLACED（装新 APK）连进程都不保留。两种情况用户看到的现象完全一样：
 * 通知栏空空、后台一条提醒都没有，直到手动点开 app。这里补上这两条重启路径。</p>
 *
 * <p>只负责「拉起来」，不负责配置：目标地址、节点表、token 全在 SharedPreferences 里，
 * 上一次 configure 已经写好了。没配过就什么都不做 —— 不能开着前台服务去连不存在的地址。</p>
 */
public class ReplyPollerBootReceiver extends BroadcastReceiver {
    private static final String TAG = "MindFSReplyPoller";

    @Override
    public void onReceive(Context context, Intent intent) {
        String action = intent == null ? "" : intent.getAction();
        if (!Intent.ACTION_BOOT_COMPLETED.equals(action)
            && !Intent.ACTION_MY_PACKAGE_REPLACED.equals(action)) {
            return;
        }
        if (!ReplyPollerService.isConfigured(context)) {
            Log.i(TAG, "boot receiver: reply poller is not configured yet, skipping");
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
            Log.i(TAG, "boot receiver: reply poller restarted action=" + action);
        } catch (RuntimeException ex) {
            // Android 12+ 可能在后台拒绝启动前台服务；这不是崩溃理由，
            // 下一次用户打开 app 时会正常恢复。
            Log.w(TAG, "boot receiver: failed to restart reply poller", ex);
        }
    }
}
