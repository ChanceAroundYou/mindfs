package com.mindfs.app;

import android.Manifest;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.PowerManager;
import android.provider.Settings;
import android.util.Log;
import androidx.core.content.ContextCompat;
import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

@CapacitorPlugin(
    name = "ReplyPoller",
    permissions = {
        @Permission(strings = { Manifest.permission.POST_NOTIFICATIONS }, alias = "notifications")
    }
)
public class ReplyPollerPlugin extends Plugin {
    private static final String TAG = "MindFSReplyPoller";

    @PluginMethod
    public void requestPermission(PluginCall call) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
            resolvePermission(call, PermissionState.GRANTED);
            return;
        }
        PermissionState state = getPermissionState("notifications");
        if (state == PermissionState.GRANTED) {
            resolvePermission(call, state);
            return;
        }
        requestPermissionForAlias("notifications", call, "notificationPermissionCallback");
    }

    @PermissionCallback
    private void notificationPermissionCallback(PluginCall call) {
        resolvePermission(call, getPermissionState("notifications"));
    }

    @PluginMethod
    public void configure(PluginCall call) {
        String apiBaseUrl = call.getString("apiBaseUrl", "").trim();
        if (apiBaseUrl.isEmpty()) {
            call.reject("apiBaseUrl is required");
            return;
        }
        Intent intent = new Intent(getContext(), ReplyPollerService.class);
        intent.setAction(ReplyPollerService.ACTION_CONFIGURE);
        intent.putExtra(ReplyPollerService.EXTRA_API_BASE_URL, apiBaseUrl);
        if (call.getData().has("waitUrl")) {
            // 单节点兜底的长轮询地址由前端算好（挂载前缀 + user=），原生不许自己拼。
            intent.putExtra(ReplyPollerService.EXTRA_WAIT_URL, call.getString("waitUrl", ""));
        }
        if (call.getData().has("token")) {
            intent.putExtra(ReplyPollerService.EXTRA_TOKEN, call.getString("token", ""));
        }
        if (call.getData().has("e2eeRequired")) {
            intent.putExtra(ReplyPollerService.EXTRA_E2EE_REQUIRED, call.getBoolean("e2eeRequired", false));
        }
        if (call.getData().has("e2eeNodeId")) {
            intent.putExtra(ReplyPollerService.EXTRA_E2EE_NODE_ID, call.getString("e2eeNodeId", ""));
        }
        if (call.getData().has("e2eeClientId")) {
            intent.putExtra(ReplyPollerService.EXTRA_E2EE_CLIENT_ID, call.getString("e2eeClientId", ""));
        }
        if (call.getData().has("e2eeTransportKey")) {
            intent.putExtra(ReplyPollerService.EXTRA_E2EE_TRANSPORT_KEY, call.getString("e2eeTransportKey", ""));
        }
        startServiceSafely(intent);
        call.resolve();
    }

    @PluginMethod
    public void resume(PluginCall call) {
        Intent intent = new Intent(getContext(), ReplyPollerService.class);
        intent.setAction(ReplyPollerService.ACTION_RESUME);
        startForeground(intent);
        call.resolve();
    }

    @PluginMethod
    public void pause(PluginCall call) {
        Intent intent = new Intent(getContext(), ReplyPollerService.class);
        intent.setAction(ReplyPollerService.ACTION_PAUSE);
        startServiceSafely(intent);
        call.resolve();
    }

    /**
     * 电池优化白名单状态。Doze 会掐掉后台网络与闹钟：国产 ROM 的「后台清理」更激进，
     * 用户看到的就是「后台时间一长完全不再提醒」。app 只能引导，不能代替用户点。
     */
    @PluginMethod
    public void batteryStatus(PluginCall call) {
        JSObject result = new JSObject();
        result.put("ignoring", isIgnoringBatteryOptimizations());
        call.resolve(result);
    }

    /** 跳系统确认框申请「不受电池优化限制」。用户拒绝时只是回到原页面，不报错。 */
    @PluginMethod
    public void requestBatteryExemption(PluginCall call) {
        JSObject result = new JSObject();
        result.put("ignoring", isIgnoringBatteryOptimizations());
        if (!isIgnoringBatteryOptimizations()) {
            try {
                Intent intent = new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS);
                intent.setData(Uri.parse("package:" + getContext().getPackageName()));
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                getContext().startActivity(intent);
            } catch (RuntimeException ex) {
                // 部分 ROM 没有这个 activity：退回电池优化总列表，至少让用户能自己关。
                try {
                    Intent fallback = new Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS);
                    fallback.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                    getContext().startActivity(fallback);
                } catch (RuntimeException ignored) {
                    result.put("error", "battery_settings_unavailable");
                }
            }
        }
        call.resolve(result);
    }

    private boolean isIgnoringBatteryOptimizations() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) {
            return true;
        }
        PowerManager pm = (PowerManager) getContext().getSystemService(Context.POWER_SERVICE);
        return pm != null && pm.isIgnoringBatteryOptimizations(getContext().getPackageName());
    }

    @PluginMethod
    public void testAlert(PluginCall call) {
        JSObject result = new JSObject();
        result.put("error", ReplyPollerService.postTestAlert(getContext()));
        result.put("diag", ReplyPollerService.describeNotificationState(getContext()));
        call.resolve(result);
    }

    @PluginMethod
    public void debugStatus(PluginCall call) {
        JSObject result = new JSObject();
        result.put("debug", ReplyPollerService.readPollDebug(getContext()));
        call.resolve(result);
    }

    @PluginMethod
    public void dismiss(PluginCall call) {
        String nodeId = call.getString("nodeId", "");
        String rootId = call.getString("rootId", "");
        String sessionKey = call.getString("sessionKey", "");
        android.app.NotificationManager nm =
            (android.app.NotificationManager) getContext().getSystemService(Context.NOTIFICATION_SERVICE);
        int id = ReplyPollerService.notificationIdFor(nodeId, rootId, sessionKey);
        nm.cancel(id);
        // 兼容旧版仅按 sessionKey 的通知 id
        nm.cancel(760000 + Math.abs(sessionKey.hashCode() % 100000));
        call.resolve();
    }

    @PluginMethod
    public void stop(PluginCall call) {
        Intent intent = new Intent(getContext(), ReplyPollerService.class);
        intent.setAction(ReplyPollerService.ACTION_STOP);
        startServiceSafely(intent);
        call.resolve();
    }

    // Android 8+ 在应用已进后台时 startService 会抛 IllegalStateException，12+ 对前台
    // 服务还会抛 ForegroundServiceStartNotAllowedException。插件方法是从 WebView 里调的，
    // 抛出去就是一个 JS 层看不到的进程崩溃 —— 连带把常驻轮询一起带走。
    private void startServiceSafely(Intent intent) {
        try {
            getContext().startService(intent);
        } catch (RuntimeException ex) {
            Log.w(TAG, "reply poller service start failed action=" + intent.getAction(), ex);
        }
    }

    private void startForeground(Intent intent) {
        Context context = getContext();
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                ContextCompat.startForegroundService(context, intent);
            } else {
                context.startService(intent);
            }
        } catch (RuntimeException ex) {
            Log.w(TAG, "reply poller foreground start failed action=" + intent.getAction(), ex);
        }
    }

    private void resolvePermission(PluginCall call, PermissionState state) {
        JSObject result = new JSObject();
        result.put("state", state == null ? "prompt" : state.toString());
        result.put("granted", state == PermissionState.GRANTED);
        call.resolve(result);
    }
}
