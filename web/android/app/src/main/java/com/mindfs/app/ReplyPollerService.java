package com.mindfs.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.Manifest;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.content.pm.ServiceInfo;
import android.media.AudioAttributes;
import android.net.Uri;
import android.os.Build;
import android.os.IBinder;
import androidx.core.content.ContextCompat;
import android.os.VibrationEffect;
import android.os.Vibrator;
import android.os.VibratorManager;
import android.text.TextUtils;
import android.util.Base64;
import android.util.Log;
import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.security.cert.X509Certificate;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.Date;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.TimeZone;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;
import javax.crypto.Cipher;
import javax.crypto.Mac;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import javax.net.ssl.HostnameVerifier;
import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSession;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;
import org.json.JSONArray;
import org.json.JSONObject;

public class ReplyPollerService extends Service {
    static final String ACTION_CONFIGURE = "com.mindfs.app.reply_poller.CONFIGURE";
    static final String ACTION_RESUME = "com.mindfs.app.reply_poller.RESUME";
    static final String ACTION_PAUSE = "com.mindfs.app.reply_poller.PAUSE";
    static final String ACTION_CLEAR_COMPLETED = "com.mindfs.app.reply_poller.CLEAR_COMPLETED";
    static final String ACTION_STOP = "com.mindfs.app.reply_poller.STOP";
    static final String EXTRA_API_BASE_URL = "apiBaseUrl";
    static final String EXTRA_TOKEN = "token";
    static final String EXTRA_E2EE_REQUIRED = "e2eeRequired";
    static final String EXTRA_E2EE_NODE_ID = "e2eeNodeId";
    static final String EXTRA_E2EE_CLIENT_ID = "e2eeClientId";
    static final String EXTRA_E2EE_TRANSPORT_KEY = "e2eeTransportKey";

    private static final String TAG = "MindFSReplyPoller";
    private static final String PREFS_NAME = "mindfs_reply_poller";
    private static final String PREF_API_BASE_URL = "api_base_url";
    private static final String PREF_TOKEN = "token";
    private static final String PREF_E2EE_REQUIRED = "e2ee_required";
    private static final String PREF_E2EE_NODE_ID = "e2ee_node_id";
    private static final String PREF_E2EE_CLIENT_ID = "e2ee_client_id";
    private static final String PREF_E2EE_TRANSPORT_KEY = "e2ee_transport_key";
    private static final String PREF_AWAITING_NOTIFIED = "awaiting_notified";
    private static final String PREF_DEBUG = "debug_status";
    private static final String E2EE_HEADER = "X-MindFS-E2EE";
    private static final String CLIENT_ID_HEADER = "X-MindFS-Client-ID";
    private static final String PROOF_HEADER = "X-MindFS-Proof";
    private static final String TS_HEADER = "X-MindFS-TS";
    private static final String SERVICE_CHANNEL_ID = "mindfs_reply_service_v1";
    private static final String VISIBLE_PROGRESS_CHANNEL_ID = SERVICE_CHANNEL_ID;
    private static final String PROGRESS_CHANNEL_ID = SERVICE_CHANNEL_ID;
    // v5：渠道一旦建出来，重要级别与开关就由系统记住、App 改不动了。
    // v4 在很多机器上被静音过，新建 v5 才能拿到干净的 HIGH 渠道。
    private static final String ALERT_CHANNEL_ID = "mindfs_reply_alert_v5";
    private static final String REPLY_GROUP_KEY = "com.mindfs.app.REPLY_NOTIFICATIONS";
    private static final int FOREGROUND_ID = 750001;
    private static final int NOTIFICATION_BASE_ID = 760000;
    private static final long ACTIVE_POLL_INTERVAL_SECONDS = 5;
    private static final long IDLE_POLL_INTERVAL_SECONDS = 5;
    private static final long[] ALERT_VIBRATION_PATTERN = new long[] {0, 160, 110, 160};

    private final Map<String, ReplyState> states = new HashMap<>();
    private final Map<Integer, String> notificationSignatures = new HashMap<>();
    private NotificationManager notificationManager;
    private ScheduledExecutorService executor;
    private ScheduledFuture<?> pollingTask;
    private volatile boolean running = false;
    private String apiBaseUrl = "";
    private String token = "";
    private boolean e2eeRequired = false;
    private String e2eeNodeId = "";
    private String e2eeClientId = "";
    private String e2eeTransportKey = "";
    private NotificationMode notificationMode = NotificationMode.NONE;
    private static SSLSocketFactory localTrustAllSocketFactory;

    @Override
    public void onCreate() {
        super.onCreate();
        notificationManager = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
        executor = Executors.newSingleThreadScheduledExecutor();
        createChannels();
        loadConfig();
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent == null ? "" : intent.getAction();
        if (ACTION_CONFIGURE.equals(action)) {
            configure(intent);
            return START_NOT_STICKY;
        }
        if (ACTION_RESUME.equals(action)) {
            resumePolling();
            return START_STICKY;
        }
        if (ACTION_PAUSE.equals(action)) {
            pausePolling();
            return START_NOT_STICKY;
        }
        if (ACTION_STOP.equals(action)) {
            stopAll();
            return START_NOT_STICKY;
        }
        return START_NOT_STICKY;
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public void onDestroy() {
        stopPollingTask();
        if (executor != null) {
            executor.shutdownNow();
        }
        super.onDestroy();
    }

    private void configure(Intent intent) {
        String nextApiBaseUrl = safe(intent.getStringExtra(EXTRA_API_BASE_URL)).replaceAll("/+$", "");
        if (isLocalShellOrigin(nextApiBaseUrl)) {
            Log.w(TAG, "ignore local shell apiBaseUrl=" + nextApiBaseUrl);
            nextApiBaseUrl = apiBaseUrl;
        }
        SharedPreferences.Editor editor = getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit();
        if (!nextApiBaseUrl.isEmpty()) {
            apiBaseUrl = nextApiBaseUrl;
            editor.putString(PREF_API_BASE_URL, apiBaseUrl);
        }
        if (intent.hasExtra(EXTRA_TOKEN)) {
            token = safe(intent.getStringExtra(EXTRA_TOKEN));
            editor.putString(PREF_TOKEN, token);
        }
        if (intent.hasExtra(EXTRA_E2EE_REQUIRED)) {
            e2eeRequired = intent.getBooleanExtra(EXTRA_E2EE_REQUIRED, false);
            editor.putBoolean(PREF_E2EE_REQUIRED, e2eeRequired);
        }
        if (intent.hasExtra(EXTRA_E2EE_NODE_ID)) {
            e2eeNodeId = safe(intent.getStringExtra(EXTRA_E2EE_NODE_ID));
            editor.putString(PREF_E2EE_NODE_ID, e2eeNodeId);
        }
        if (intent.hasExtra(EXTRA_E2EE_CLIENT_ID)) {
            e2eeClientId = safe(intent.getStringExtra(EXTRA_E2EE_CLIENT_ID));
            editor.putString(PREF_E2EE_CLIENT_ID, e2eeClientId);
        }
        if (intent.hasExtra(EXTRA_E2EE_TRANSPORT_KEY)) {
            e2eeTransportKey = safe(intent.getStringExtra(EXTRA_E2EE_TRANSPORT_KEY));
            editor.putString(PREF_E2EE_TRANSPORT_KEY, e2eeTransportKey);
        }
        editor.apply();
        Log.i(TAG, "configured apiBaseUrl=" + apiBaseUrl + " e2eeRequired=" + e2eeRequired + " e2eeReady=" + hasE2EESession());
    }

    private void loadConfig() {
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        apiBaseUrl = safe(prefs.getString(PREF_API_BASE_URL, ""));
        token = safe(prefs.getString(PREF_TOKEN, ""));
        e2eeRequired = prefs.getBoolean(PREF_E2EE_REQUIRED, false);
        e2eeNodeId = safe(prefs.getString(PREF_E2EE_NODE_ID, ""));
        e2eeClientId = safe(prefs.getString(PREF_E2EE_CLIENT_ID, ""));
        e2eeTransportKey = safe(prefs.getString(PREF_E2EE_TRANSPORT_KEY, ""));
    }

    private boolean isLocalShellOrigin(String value) {
        if (value == null || value.trim().isEmpty()) {
            return false;
        }
        try {
            URL url = new URL(value);
            String protocol = safe(url.getProtocol());
            String host = safe(url.getHost());
            return "http".equalsIgnoreCase(protocol) &&
                ("localhost".equalsIgnoreCase(host) || "127.0.0.1".equals(host) || "::1".equals(host));
        } catch (Exception ignored) {
            return false;
        }
    }

    private void resumePolling() {
        loadConfig();
        if (apiBaseUrl.isEmpty()) {
            Log.w(TAG, "skip resume: apiBaseUrl is empty");
            stopSelf();
            return;
        }
        Log.i(TAG, "resume polling apiBaseUrl=" + apiBaseUrl);
        running = true;
        startForegroundCompat(FOREGROUND_ID, buildIdleNotification());
        scheduleNextPoll(0);
    }

    private void pausePolling() {
        Log.i(TAG, "pause polling");
        running = false;
        stopPollingTask();
        stopForegroundCompat();
        stopSelf();
    }

    private void stopAll() {
        running = false;
        stopPollingTask();
        for (ReplyState state : states.values()) {
            notificationManager.cancel(state.notificationId);
        }
        Set<String> notified = getSharedPreferences(PREFS_NAME, MODE_PRIVATE).getStringSet(PREF_AWAITING_NOTIFIED, new HashSet<>());
        for (String k : notified) {
            String[] parts = k.split("\u001f", 2);
            String rid = parts.length > 0 ? parts[0] : "";
            String sk = parts.length > 1 ? parts[1] : "";
            notificationManager.cancel(notificationIdFor(rid, sk));
        }
        notificationManager.cancel(FOREGROUND_ID);
        notificationSignatures.clear();
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit().remove(PREF_AWAITING_NOTIFIED).apply();
        states.clear();
        stopForegroundCompat();
        stopSelf();
    }

    private void stopPollingTask() {
        if (pollingTask != null) {
            pollingTask.cancel(true);
            pollingTask = null;
        }
    }

    private void pollOnceSafely() {
        if (!running) {
            return;
        }
        long nextDelaySeconds = IDLE_POLL_INTERVAL_SECONDS;
        try {
            Map<String, ReplyState> next = fetchReplyingSessions();
            recordPollDebug(true, next.size(), "");
            nextDelaySeconds = applyReplyingSessions(next);
        } catch (Exception ex) {
            Log.w(TAG, "poll failed", ex);
            recordPollDebug(false, 0, ex.getClass().getSimpleName() + ": " + ex.getMessage());
            nextDelaySeconds = IDLE_POLL_INTERVAL_SECONDS;
        } finally {
            scheduleNextPoll(nextDelaySeconds);
        }
    }

    // 轮询是后台黑盒：把「最近一次成功/失败、拿到几个会话、打的哪个地址」落到 prefs，
    // 供关于弹窗里的诊断行读出来。没有它，用户只看到「没有提醒」，没法判断是
    // 服务没跑、地址打错、账号不对还是接口 401。
    private void recordPollDebug(boolean ok, int sessionCount, String error) {
        JSONObject o = new JSONObject();
        try {
            o.put("at", new SimpleDateFormat("MM-dd HH:mm:ss", Locale.US).format(new Date()));
            o.put("ok", ok);
            o.put("running", running);
            o.put("sessions", sessionCount);
            o.put("apiBaseUrl", apiBaseUrl);
            o.put("error", error == null ? "" : error);
        } catch (Exception ignored) {
            // JSONObject.put 只在键为 null 时抛，这里不可能发生；真出错就保留上一条记录。
        }
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit().putString(PREF_DEBUG, o.toString()).apply();
    }

    // 前端上报的地址才是准的（Vite 的 BASE_URL 带 /mindfs 这类挂载前缀）；
    // 原生侧从 WebView 的 URL 只能推出 origin。已经配过就别再覆盖。
    static boolean hasConfiguredBase(Context context) {
        String stored = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
            .getString(PREF_API_BASE_URL, "");
        return stored != null && !stored.trim().isEmpty();
    }

    static String readPollDebug(Context context) {
        return context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getString(PREF_DEBUG, "");
    }

    private void scheduleNextPoll(long delaySeconds) {
        if (!running || executor == null || executor.isShutdown()) {
            return;
        }
        if (pollingTask != null && !pollingTask.isDone() && !pollingTask.isCancelled()) {
            pollingTask.cancel(false);
        }
        pollingTask = executor.schedule(this::pollOnceSafely, delaySeconds, TimeUnit.SECONDS);
    }

    private Map<String, ReplyState> fetchReplyingSessions() throws Exception {
        URL url = new URL(apiBaseUrl + "/api/replying-sessions");
        HttpURLConnection conn = (HttpURLConnection) url.openConnection();
        if (conn instanceof HttpsURLConnection && MainActivity.isLocalNetworkHost(url.getHost())) {
            HttpsURLConnection https = (HttpsURLConnection) conn;
            https.setSSLSocketFactory(localTrustAllSocketFactory());
            https.setHostnameVerifier(new HostnameVerifier() {
                @Override
                public boolean verify(String hostname, SSLSession session) {
                    return MainActivity.isLocalNetworkHost(hostname);
                }
            });
        }
        conn.setRequestMethod("GET");
        conn.setConnectTimeout(5000);
        conn.setReadTimeout(8000);
        conn.setRequestProperty("Accept", "application/json");
        if (!token.isEmpty()) {
            conn.setRequestProperty("Authorization", "Bearer " + token);
        }
        if (e2eeRequired) {
            if (!hasE2EESession()) {
                conn.disconnect();
                throw new IllegalStateException("e2ee session missing");
            }
            conn.setRequestProperty(E2EE_HEADER, "1");
            conn.setRequestProperty(CLIENT_ID_HEADER, e2eeClientId);
            addRequestProofHeaders(conn, "GET", url);
        }
        int status = conn.getResponseCode();
        InputStream stream = status >= 200 && status < 300 ? conn.getInputStream() : conn.getErrorStream();
        String body = readAll(stream);
        boolean protectedResponse = "1".equals(safe(conn.getHeaderField(E2EE_HEADER)));
        conn.disconnect();
        if (status < 200 || status >= 300) {
            if (status == 401 && e2eeRequired) {
                clearE2EESession();
            }
            throw new IllegalStateException("HTTP " + status + ": " + body);
        }
        if (protectedResponse) {
            body = decryptProtectedBody(body);
        }

        JSONObject root = new JSONObject(body);
        JSONArray sessions = root.optJSONArray("sessions");
        Map<String, ReplyState> next = new HashMap<>();
        if (sessions == null) {
            return next;
        }
        for (int i = 0; i < sessions.length(); i++) {
            JSONObject item = sessions.optJSONObject(i);
            if (item == null) {
                continue;
            }
            String sessionKey = safe(item.optString("sessionKey", ""));
            if (sessionKey.isEmpty()) {
                continue;
            }
            String rootId = safe(item.optString("rootId", ""));
            ReplyState state = new ReplyState(sessionKey, rootId);
            state.rootId = rootId;
            state.rootTitle = safe(item.optString("rootTitle", ""));
            state.sessionTitle = safe(item.optString("sessionTitle", ""));
            state.summary = safe(item.optString("summary", ""));
            state.updatedAt = safe(item.optString("updatedAt", ""));
            String st = safe(item.optString("status", ""));
            state.replying = "replying".equalsIgnoreCase(st) || item.optBoolean("active", false);
            state.awaiting = item.optBoolean("awaiting_answer", false) || item.optBoolean("awaitingAnswer", false);
            state.status = state.replying ? ReplyStatus.REPLYING : ReplyStatus.COMPLETED;
            Log.i(TAG, "fetched session=" + sessionKey + " summaryLength=" + state.summary.length());
            next.put(sessionKey, state);
        }
        return next;
    }

    private boolean hasE2EESession() {
        return !e2eeClientId.isEmpty() && !e2eeTransportKey.isEmpty();
    }

    private void clearE2EESession() {
        e2eeClientId = "";
        e2eeTransportKey = "";
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
            .edit()
            .remove(PREF_E2EE_CLIENT_ID)
            .remove(PREF_E2EE_TRANSPORT_KEY)
            .apply();
    }

    private String decryptProtectedBody(String body) throws Exception {
        if (!hasE2EESession()) {
            throw new IllegalStateException("e2ee session missing");
        }
        JSONObject envelope = new JSONObject(body);
        byte[] key = Base64.decode(e2eeTransportKey, Base64.DEFAULT);
        byte[] nonce = Base64.decode(envelope.optString("nonce", ""), Base64.DEFAULT);
        byte[] ciphertext = Base64.decode(envelope.optString("ciphertext", ""), Base64.DEFAULT);
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, new SecretKeySpec(key, "AES"), new GCMParameterSpec(128, nonce));
        byte[] plaintext = cipher.doFinal(ciphertext);
        return new String(plaintext, StandardCharsets.UTF_8);
    }

    private void addRequestProofHeaders(HttpURLConnection conn, String method, URL url) throws Exception {
        String ts = utcTimestamp();
        String proofPath = requestProofPath(url);
        byte[] key = Base64.decode(e2eeTransportKey, Base64.DEFAULT);
        String proof = buildRequestProof(key, method, proofPath, ts, e2eeClientId);
        conn.setRequestProperty(TS_HEADER, ts);
        conn.setRequestProperty(PROOF_HEADER, proof);
    }

    private static String utcTimestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date());
    }

    private static String requestProofPath(URL url) {
        String path = safe(url == null ? "" : url.getPath());
        if (path.isEmpty()) {
            path = "/";
        }
        path = path.replaceFirst("^/n/[^/]+", "");
        if (path.isEmpty()) {
            path = "/";
        }
        String query = url == null ? "" : safe(url.getQuery());
        return query.isEmpty() ? path : path + "?" + query;
    }

    private static String buildRequestProof(byte[] key, String method, String path, String ts, String clientId) throws Exception {
        String joined = "mindfs-request-proof\u001f" + method + "\u001f" + path + "\u001f" + ts + "\u001f" + clientId;
        MessageDigest digest = MessageDigest.getInstance("SHA-256");
        byte[] sum = digest.digest(joined.getBytes(StandardCharsets.UTF_8));
        Mac hmac = Mac.getInstance("HmacSHA256");
        hmac.init(new SecretKeySpec(key, "HmacSHA256"));
        return Base64.encodeToString(hmac.doFinal(sum), Base64.NO_WRAP);
    }

    private static synchronized SSLSocketFactory localTrustAllSocketFactory() throws Exception {
        if (localTrustAllSocketFactory != null) {
            return localTrustAllSocketFactory;
        }
        TrustManager[] trustManagers = new TrustManager[] {
            new X509TrustManager() {
                @Override
                public void checkClientTrusted(X509Certificate[] chain, String authType) {}

                @Override
                public void checkServerTrusted(X509Certificate[] chain, String authType) {}

                @Override
                public X509Certificate[] getAcceptedIssuers() {
                    return new X509Certificate[0];
                }
            },
        };
        SSLContext context = SSLContext.getInstance("TLS");
        context.init(null, trustManagers, new SecureRandom());
        localTrustAllSocketFactory = context.getSocketFactory();
        return localTrustAllSocketFactory;
    }

    private long applyReplyingSessions(Map<String, ReplyState> next) {
        if (!running) {
            return IDLE_POLL_INTERVAL_SECONDS;
        }
        Log.i(TAG, "poll success sessions=" + next.size());
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        Set<String> awaitingNotified = new HashSet<>(prefs.getStringSet(PREF_AWAITING_NOTIFIED, new HashSet<>()));

        // 1. 合并 incoming
        Set<String> currentKeys = new HashSet<>();
        for (ReplyState incoming : next.values()) {
            currentKeys.add(incoming.sessionKey);
            ReplyState state = states.get(incoming.sessionKey);
            if (state == null) {
                state = incoming;
                states.put(state.sessionKey, state);
            } else {
                state.rootId = incoming.rootId;
                state.rootTitle = incoming.rootTitle;
                state.sessionTitle = incoming.sessionTitle;
                if (!incoming.summary.isEmpty()) {
                    state.summary = incoming.summary;
                }
                state.updatedAt = incoming.updatedAt;
                state.replying = incoming.replying;
                state.awaiting = incoming.awaiting;
                state.status = incoming.replying ? ReplyStatus.REPLYING : ReplyStatus.COMPLETED;
            }
        }

        // 2. DONE 检测：上一轮 replying、本轮消失
        for (ReplyState state : new ArrayList<>(states.values())) {
            if (state.status == ReplyStatus.REPLYING && !currentKeys.contains(state.sessionKey)) {
                state.status = ReplyStatus.COMPLETED;
                state.replying = false;
                postAlert("done", state);
                vibrateReplyCompletion();
            }
        }

        // 3. awaiting 生命周期（用 planner）
        List<ReplyAlertPlanner.Snapshot> snaps = new ArrayList<>();
        for (ReplyState st : states.values()) {
            if (currentKeys.contains(st.sessionKey)) {
                ReplyAlertPlanner.Snapshot sn = new ReplyAlertPlanner.Snapshot();
                sn.rootId = st.rootId; sn.sessionKey = st.sessionKey;
                sn.rootTitle = st.rootTitle; sn.sessionTitle = st.sessionTitle;
                sn.summary = st.summary; sn.updatedAt = st.updatedAt;
                sn.replying = st.replying; sn.awaiting = st.awaiting;
                snaps.add(sn);
            }
        }
        ReplyAlertPlanner plan = new ReplyAlertPlanner();
        ReplyAlertPlanner.Plan p = plan.apply(snaps, awaitingNotified);
        for (ReplyAlertPlanner.Alert a : p.post) {
            ReplyState st = findState(a.rootId, a.sessionKey);
            if (st != null) postAlert(a.kind, st);
        }
        for (String k : p.cancel) {
            String[] parts = k.split("\u001f", 2);
            String rid = parts.length > 0 ? parts[0] : "";
            String sk = parts.length > 1 ? parts[1] : "";
            notificationManager.cancel(notificationIdFor(rid, sk));
            // 兼容旧 id
            notificationManager.cancel(NOTIFICATION_BASE_ID + Math.abs(sk.hashCode() % 100000));
        }
        prefs.edit().putStringSet(PREF_AWAITING_NOTIFIED, p.awaitingNotified).apply();

        // 4. 前台服务通知（极简/聚合）
        List<ReplyState> live = new ArrayList<>();
        for (ReplyState st : states.values()) {
            if (st.replying || st.awaiting) live.add(st);
        }
        updateForegroundNotification(live);

        if (currentKeys.isEmpty() && live.isEmpty()) {
            stopForegroundCompat();
            return IDLE_POLL_INTERVAL_SECONDS;
        }
        return currentKeys.isEmpty() ? IDLE_POLL_INTERVAL_SECONDS : ACTIVE_POLL_INTERVAL_SECONDS;
    }

    private ReplyState findState(String rootId, String sessionKey) {
        for (ReplyState st : states.values()) {
            if (st.sessionKey.equals(sessionKey) && (rootId == null || rootId.equals(st.rootId))) {
                return st;
            }
        }
        return null;
    }

    // 通知到底是被系统挡了还是投出去了：总开关、渠道等级、通知栏里有没有这一条、勿扰模式。
    // 用户那边「已发送但什么都没有」全靠这几项区分。
    static String describeNotificationState(Context context) {
        NotificationManager nm = (NotificationManager) context.getSystemService(Context.NOTIFICATION_SERVICE);
        if (nm == null) {
            return "通知服务不可用";
        }
        StringBuilder sb = new StringBuilder();
        sb.append("通知总开关=").append(nm.areNotificationsEnabled() ? "开" : "关");
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = nm.getNotificationChannel(ALERT_CHANNEL_ID);
            if (channel == null) {
                sb.append(" · 渠道=缺失");
            } else {
                sb.append(" · 渠道=").append(channel.getImportance() == NotificationManager.IMPORTANCE_NONE
                    ? "关闭" : "等级" + channel.getImportance());
            }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            android.service.notification.StatusBarNotification[] active = nm.getActiveNotifications();
            boolean mine = false;
            for (android.service.notification.StatusBarNotification statusBar : active) {
                if (statusBar != null && statusBar.getId() == 799999) {
                    mine = true;
                }
            }
            sb.append(" · 通知栏=").append(active.length).append(" 条").append(mine ? "（含测试提醒）" : "（不含测试提醒）");
        }
        sb.append(" · 勿扰=").append(interruptionLabel(nm.getCurrentInterruptionFilter()));
        return sb.toString();
    }

    private static String interruptionLabel(int filter) {
        switch (filter) {
            case NotificationManager.INTERRUPTION_FILTER_ALL: return "关";
            case NotificationManager.INTERRUPTION_FILTER_PRIORITY: return "仅优先";
            case NotificationManager.INTERRUPTION_FILTER_NONE: return "全静音";
            case NotificationManager.INTERRUPTION_FILTER_ALARMS: return "仅闹钟";
            default: return "未知";
        }
    }

    // 返回空串 = 已投递；非空 = 需要展示给用户的原因（权限、渠道被关、投递异常）。
    public static String postTestAlert(Context context) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU
            && ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
            return "通知权限未授予：请在系统设置里允许 MindFS 发送通知";
        }
        ensureChannels(context);
        NotificationManager nm = (NotificationManager) context.getSystemService(Context.NOTIFICATION_SERVICE);
        if (nm == null) {
            return "系统通知服务不可用";
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = nm.getNotificationChannel(ALERT_CHANNEL_ID);
            if (channel == null) {
                return "提醒渠道创建失败，请重启应用后重试";
            }
            if (channel.getImportance() == NotificationManager.IMPORTANCE_NONE) {
                return "「MindFS 任务提醒」渠道被关闭，请在系统设置里重新打开";
            }
        }
        // autoCancel 只在通知带 contentIntent 时才生效：没有 PendingIntent 的点击
        // 不会触发任何事务，系统也就没有机会把它撤掉（点完一直挂在通知栏）。
        Intent launch = context.getPackageManager().getLaunchIntentForPackage(context.getPackageName());
        Notification.Builder b = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(context, ALERT_CHANNEL_ID)
            : new Notification.Builder(context);
        if (launch != null) {
            launch.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
            b.setContentIntent(PendingIntent.getActivity(
                context, 799999, launch,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE));
        }
        b.setSmallIcon(R.drawable.ic_stat_mindfs_notification)
         .setContentTitle("MindFS 测试提醒")
         .setContentText("通知链路正常。")
         .setAutoCancel(true)
         .setCategory(Notification.CATEGORY_STATUS);
        try {
            nm.notify(799999, b.build());
        } catch (Exception ex) {
            return "通知投递失败：" + ex.getMessage();
        }
        return "";
    }

    private void postAlert(String kind, ReplyState state) {
        String title = buildReplyTitlePrefix(state) + " · " + ("awaiting".equals(kind) ? "需要你回答" : "完成");
        String text = state.summary;
        Intent launchIntent = getPackageManager().getLaunchIntentForPackage(getPackageName());
        if (launchIntent == null) launchIntent = new Intent(this, MainActivity.class);
        launchIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        launchIntent.putExtra("rootId", state.rootId);
        launchIntent.putExtra("sessionKey", state.sessionKey);
        int id = state.notificationId;
        PendingIntent pi = PendingIntent.getActivity(this, id, launchIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification.Builder b = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? new Notification.Builder(this, ALERT_CHANNEL_ID)
                : new Notification.Builder(this);
        b.setSmallIcon(R.drawable.ic_stat_mindfs_notification)
         .setContentTitle(title)
         .setContentText(text)
         .setContentIntent(pi)
         .setAutoCancel(true)
         .setOngoing(false)
         .setShowWhen(true)
         .setVisibility(Notification.VISIBILITY_PUBLIC)
         .setCategory(Notification.CATEGORY_STATUS)
         .setStyle(new Notification.BigTextStyle().bigText(text));
        if (!TextUtils.isEmpty(text)) b.setTicker(text);
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            b.setSound(replyAlertSoundUri(this));
        }
        notificationManager.notify(id, b.build());
    }

    private void updateForegroundNotification(List<ReplyState> live) {
        if (live == null || live.isEmpty()) {
            // 极简静默
            try { startForegroundCompat(FOREGROUND_ID, buildIdleNotification()); }
            catch (RuntimeException ex) { notificationManager.notify(FOREGROUND_ID, buildIdleNotification()); }
            return;
        }
        // 常驻通知只报状态，不带项目/会话名：它整天挂在通知栏，名字属于隐私噪音。
        String text = live.size() == 1 ? "有会话正在回复" : live.size() + " 个会话回复中";
        Notification n = buildIdleNotification(text);
        try { startForegroundCompat(FOREGROUND_ID, n); }
        catch (RuntimeException ex) { notificationManager.notify(FOREGROUND_ID, n); }
    }

    private Notification buildIdleNotification() { return buildIdleNotification("MindFS"); }
    private Notification buildIdleNotification(String text) {
        Notification.Builder b = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? new Notification.Builder(this, SERVICE_CHANNEL_ID)
                : new Notification.Builder(this);
        b.setSmallIcon(R.drawable.ic_stat_mindfs_notification)
         .setContentTitle(text == null || text.isEmpty() ? "MindFS" : text)
         .setContentText("")
         .setOngoing(true)
         .setOnlyAlertOnce(true)
         .setShowWhen(false)
         .setVisibility(Notification.VISIBILITY_SECRET)
         .setCategory(Notification.CATEGORY_SERVICE);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            b.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE);
        }
        return b.build();
    }

    private void showReplyNotifications(List<ReplyState> states) {
        if (states == null || states.isEmpty()) {
            return;
        }
        sortMostRecentFirst(states);
        if (states.size() == 1) {
            showSingleReplyNotification(states.get(0));
            return;
        }
        boolean hasReplying = hasReplyingState(states);
        if (notificationMode != NotificationMode.GROUP) {
            notificationManager.cancel(FOREGROUND_ID);
            notificationSignatures.remove(FOREGROUND_ID);
        }
        notificationMode = NotificationMode.GROUP;
        Notification summary = buildReplyGroupSummaryNotification(states);
        boolean foregroundStarted = false;
        if (!hasReplying) {
            notifyIfChanged(FOREGROUND_ID, summary, buildGroupSummarySignature(states));
        } else {
            try {
                startForegroundIfChanged(FOREGROUND_ID, summary, buildGroupSummarySignature(states));
                foregroundStarted = true;
            } catch (RuntimeException ex) {
                Log.w(TAG, "start foreground failed, posting notification only", ex);
                notifyIfChanged(FOREGROUND_ID, summary, buildGroupSummarySignature(states));
            }
        }
        for (ReplyState state : states) {
            notifyIfChanged(state.notificationId, buildReplyChildNotification(state), buildStateSignature(state, true));
        }
        if (!foregroundStarted && hasReplying) {
            notifyIfChanged(FOREGROUND_ID, summary, buildGroupSummarySignature(states));
        }
    }

    private void showSingleReplyNotification(ReplyState state) {
        if (notificationMode != NotificationMode.SINGLE) {
            for (ReplyState item : states.values()) {
                notificationManager.cancel(item.notificationId);
                notificationSignatures.remove(item.notificationId);
            }
        }
        notificationMode = NotificationMode.SINGLE;
        boolean replying = state.status == ReplyStatus.REPLYING;
        Notification notification = buildReplyNotification(state);
        String signature = buildStateSignature(state, false);
        if (!replying) {
            notifyIfChanged(FOREGROUND_ID, notification, signature);
            return;
        }
        try {
            startForegroundIfChanged(FOREGROUND_ID, notification, signature);
        } catch (RuntimeException ex) {
            Log.w(TAG, "start foreground failed, posting notification only", ex);
            notifyIfChanged(FOREGROUND_ID, notification, signature);
        }
    }

    private void notifyIfChanged(int notificationId, Notification notification, String signature) {
        if (signature.equals(notificationSignatures.get(notificationId))) {
            return;
        }
        notificationManager.notify(notificationId, notification);
        notificationSignatures.put(notificationId, signature);
    }

    private void startForegroundIfChanged(int notificationId, Notification notification, String signature) {
        if (signature.equals(notificationSignatures.get(notificationId))) {
            return;
        }
        startForegroundCompat(notificationId, notification);
        notificationSignatures.put(notificationId, signature);
    }

    private String buildGroupSummarySignature(List<ReplyState> states) {
        StringBuilder builder = new StringBuilder(hasReplyingState(states) ? "group:replying" : "group:done");
        builder.append(':').append(states.size());
        for (ReplyState state : states) {
            builder.append('|').append(buildStateSignature(state, true));
        }
        return builder.toString();
    }

    private String buildStateSignature(ReplyState state, boolean grouped) {
        return (grouped ? "child" : "single") +
            ":" + state.sessionKey +
            ":" + state.rootId +
            ":" + state.rootTitle +
            ":" + state.sessionTitle +
            ":" + state.summary +
            ":" + state.status.name();
    }

    private List<ReplyState> visibleStates() {
        List<ReplyState> items = new ArrayList<>();
        for (ReplyState state : states.values()) {
            if (state.status == ReplyStatus.REPLYING || state.status == ReplyStatus.COMPLETED) {
                items.add(state);
            }
        }
        return items;
    }

    private void sortMostRecentFirst(List<ReplyState> items) {
        Collections.sort(items, new Comparator<ReplyState>() {
            @Override
            public int compare(ReplyState left, ReplyState right) {
                return right.updatedAt.compareTo(left.updatedAt);
            }
        });
    }

    private boolean hasReplyingState(List<ReplyState> states) {
        for (ReplyState state : states) {
            if (state.status == ReplyStatus.REPLYING) {
                return true;
            }
        }
        return false;
    }

    private Notification buildReplyGroupSummaryNotification(List<ReplyState> states) {
        ReplyState target = states.get(0);
        Intent launchIntent = getPackageManager().getLaunchIntentForPackage(getPackageName());
        if (launchIntent == null) {
            launchIntent = new Intent(this, MainActivity.class);
        }
        launchIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        launchIntent.putExtra("rootId", target.rootId);
        launchIntent.putExtra("sessionKey", target.sessionKey);

        PendingIntent contentIntent = PendingIntent.getActivity(
            this,
            FOREGROUND_ID,
            launchIntent,
            PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE
        );

        boolean hasReplying = hasReplyingState(states);
        String status = hasReplying ? "回复中" : "回复完成";
        String title = "MindFS";
        // 回复中的汇总常驻通知不点名；回复完成的那次保留明细。
        String text = hasReplying
            ? (states.size() == 1 ? "有会话正在回复" : states.size() + " 个会话回复中")
            : buildAggregateLine(target);
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, hasReplying ? VISIBLE_PROGRESS_CHANNEL_ID : ALERT_CHANNEL_ID)
            : new Notification.Builder(this);
        builder
            .setSmallIcon(R.drawable.ic_stat_mindfs_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(contentIntent)
            .setOngoing(hasReplying)
            .setOnlyAlertOnce(hasReplying)
            .setShowWhen(false)
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setCategory(hasReplying ? Notification.CATEGORY_SERVICE : Notification.CATEGORY_STATUS)
            .setGroup(REPLY_GROUP_KEY)
            .setGroupSummary(true)
            .setGroupAlertBehavior(Notification.GROUP_ALERT_SUMMARY)
            .setStyle(new Notification.InboxStyle().setSummaryText(status));
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O && !hasReplying) {
            builder.setSound(replyAlertSoundUri(this));
        }
        if (hasReplying) {
            builder.setStyle(new Notification.BigTextStyle().bigText(text));
        } else {
            Notification.InboxStyle style = new Notification.InboxStyle();
            for (ReplyState state : states) {
                style.addLine(buildAggregateLine(state));
            }
            style.setSummaryText(status);
            builder.setStyle(style);
        }
        if (!TextUtils.isEmpty(text)) {
            builder.setTicker(text);
        }
        return builder.build();
    }

    private String buildAggregateText(List<ReplyState> states) {
        StringBuilder builder = new StringBuilder();
        for (int i = 0; i < states.size(); i++) {
            if (i > 0) {
                builder.append('\n');
            }
            builder.append(buildAggregateLine(states.get(i)));
        }
        return builder.toString();
    }

    private String buildAggregateLine(ReplyState state) {
        String title = buildReplyTitle(state, state.status == ReplyStatus.COMPLETED ? "完成" : "回复中");
        if (state.summary.isEmpty()) {
            return title;
        }
        return title + ": " + state.summary;
    }

    private Notification buildReplyPlaceholderNotification() {
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, VISIBLE_PROGRESS_CHANNEL_ID)
            : new Notification.Builder(this);
        builder
            .setSmallIcon(R.drawable.ic_stat_mindfs_notification)
            .setContentTitle("MindFS")
            .setContentText("正在检查后台回复")
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setCategory(Notification.CATEGORY_SERVICE);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            builder.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE);
        }
        return builder.build();
    }

    private Notification buildReplyNotification(ReplyState state) {
        boolean replying = state.status == ReplyStatus.REPLYING;
        Intent launchIntent = getPackageManager().getLaunchIntentForPackage(getPackageName());
        if (launchIntent == null) {
            launchIntent = new Intent(this, MainActivity.class);
        }
        launchIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        launchIntent.putExtra("rootId", state.rootId);
        launchIntent.putExtra("sessionKey", state.sessionKey);

        PendingIntent contentIntent = PendingIntent.getActivity(
            this,
            state.notificationId,
            launchIntent,
            PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE
        );

        // 回复中 = 常驻（会被抬成前台服务通知）：只写状态，不写项目/会话名。
        // 完成那次是一次性提醒，保留名称便于辨认。
        String title = replying ? "MindFS" : buildReplyTitle(state, "完成");
        String text = replying ? "有会话正在回复" : state.summary;
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, replying ? VISIBLE_PROGRESS_CHANNEL_ID : ALERT_CHANNEL_ID)
            : new Notification.Builder(this);
        builder
            .setSmallIcon(R.drawable.ic_stat_mindfs_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(contentIntent)
            .setOngoing(replying)
            .setOnlyAlertOnce(replying)
            .setShowWhen(false)
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setCategory(replying ? Notification.CATEGORY_SERVICE : Notification.CATEGORY_STATUS)
            .setStyle(new Notification.BigTextStyle().bigText(text));
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O && !replying) {
            builder.setSound(replyAlertSoundUri(this));
        }
        if (!TextUtils.isEmpty(text)) {
            builder.setTicker(text);
        }
        return builder.build();
    }

    private Notification buildReplyChildNotification(ReplyState state) {
        boolean replying = state.status == ReplyStatus.REPLYING;
        Intent launchIntent = getPackageManager().getLaunchIntentForPackage(getPackageName());
        if (launchIntent == null) {
            launchIntent = new Intent(this, MainActivity.class);
        }
        launchIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        launchIntent.putExtra("rootId", state.rootId);
        launchIntent.putExtra("sessionKey", state.sessionKey);

        PendingIntent contentIntent = PendingIntent.getActivity(
            this,
            state.notificationId,
            launchIntent,
            PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE
        );

        String title = buildReplyTitle(state, replying ? "回复中" : "完成");
        String text = state.summary;
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, replying ? VISIBLE_PROGRESS_CHANNEL_ID : ALERT_CHANNEL_ID)
            : new Notification.Builder(this);
        builder
            .setSmallIcon(R.drawable.ic_stat_mindfs_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(contentIntent)
            .setOngoing(replying)
            .setOnlyAlertOnce(replying)
            .setShowWhen(false)
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setCategory(replying ? Notification.CATEGORY_SERVICE : Notification.CATEGORY_STATUS)
            .setGroup(REPLY_GROUP_KEY)
            .setGroupSummary(false)
            .setGroupAlertBehavior(Notification.GROUP_ALERT_SUMMARY)
            .setStyle(new Notification.BigTextStyle().bigText(text));
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O && !replying) {
            builder.setSound(replyAlertSoundUri(this));
        }
        if (!TextUtils.isEmpty(text)) {
            builder.setTicker(text);
        }
        return builder.build();
    }

    private String buildReplyTitle(ReplyState state, String status) {
        return buildReplyTitlePrefix(state) + " · " + status;
    }

    private String buildReplyTitlePrefix(ReplyState state) {
        String root = state.rootTitle.isEmpty() ? state.rootId : state.rootTitle;
        if (root.isEmpty()) {
            root = "MindFS";
        }
        String title = state.sessionTitle.isEmpty() ? "会话" : state.sessionTitle;
        return root + " · " + title;
    }

    private String buildReplyItemTitle(ReplyState state, String status) {
        String title = state.sessionTitle.isEmpty() ? "会话" : state.sessionTitle;
        return title + " · " + status;
    }

    private void createChannels() {
        ensureChannels(this);
    }

    // 渠道必须在能发通知的每个入口都建好：只在 Service.onCreate 建的话，
    // 服务没跑过时（新装/刚更新、还没切过后台）测试提醒与提醒本身都会投进不存在的渠道，
    // 系统直接静默丢弃，表现为「权限开了但什么都收不到」。
    static void ensureChannels(Context context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return;
        }
        NotificationManager notificationManager =
            (NotificationManager) context.getSystemService(Context.NOTIFICATION_SERVICE);
        if (notificationManager == null) {
            return;
        }
        NotificationChannel service = new NotificationChannel(
            SERVICE_CHANNEL_ID,
            "MindFS 后台同步",
            NotificationManager.IMPORTANCE_MIN
        );
        service.setDescription("MindFS 后台回复检查");
        service.setSound(null, null);
        service.enableVibration(false);
        service.setShowBadge(false);
        service.setLockscreenVisibility(Notification.VISIBILITY_SECRET);
        notificationManager.createNotificationChannel(service);

        NotificationChannel alert = new NotificationChannel(
            ALERT_CHANNEL_ID,
            "MindFS 任务提醒",
            NotificationManager.IMPORTANCE_HIGH
        );
        alert.setDescription("MindFS 任务完成 / 需要回答提醒");
        alert.enableVibration(true);
        alert.setVibrationPattern(ALERT_VIBRATION_PATTERN);
        alert.setSound(
            replyAlertSoundUri(context),
            new AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_NOTIFICATION)
                .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                .build()
        );
        alert.setLockscreenVisibility(Notification.VISIBILITY_PUBLIC);
        notificationManager.createNotificationChannel(alert);
    }

    private static Uri replyAlertSoundUri(Context context) {
        return Uri.parse("android.resource://" + context.getPackageName() + "/" + R.raw.mindfs_reply_alert);
    }

    private void startForegroundCompat(int notificationId, Notification notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(notificationId, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(notificationId, notification);
        }
    }

    private void stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(Service.STOP_FOREGROUND_REMOVE);
        } else {
            stopForeground(true);
        }
    }

    private void stopForegroundDetachCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(Service.STOP_FOREGROUND_DETACH);
        } else {
            stopForeground(false);
        }
    }

    private void vibrateReplyCompletion() {
        Vibrator vibrator = getReplyVibrator();
        if (vibrator == null || !vibrator.hasVibrator()) {
            return;
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            vibrator.vibrate(VibrationEffect.createWaveform(ALERT_VIBRATION_PATTERN, -1));
        } else {
            vibrator.vibrate(ALERT_VIBRATION_PATTERN, -1);
        }
    }

    private Vibrator getReplyVibrator() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            VibratorManager manager = (VibratorManager) getSystemService(Context.VIBRATOR_MANAGER_SERVICE);
            if (manager != null) {
                return manager.getDefaultVibrator();
            }
        }
        return (Vibrator) getSystemService(Context.VIBRATOR_SERVICE);
    }

    private static String readAll(InputStream stream) throws Exception {
        if (stream == null) {
            return "";
        }
        StringBuilder builder = new StringBuilder();
        try (BufferedReader reader = new BufferedReader(new InputStreamReader(stream, StandardCharsets.UTF_8))) {
            String line;
            while ((line = reader.readLine()) != null) {
                builder.append(line);
            }
        }
        return builder.toString();
    }

    private static String safe(String value) {
        return value == null ? "" : value.trim();
    }

    static int notificationIdFor(String rootId, String sessionKey) {
        String key = (rootId == null ? "" : rootId) + "\u001f" + (sessionKey == null ? "" : sessionKey);
        return NOTIFICATION_BASE_ID + Math.abs(key.hashCode() % 100000);
    }

    private enum ReplyStatus {
        REPLYING,
        COMPLETED
    }

    private enum NotificationMode {
        NONE,
        SINGLE,
        GROUP
    }

    private static final class ReplyState {
        final String sessionKey;
        final int notificationId;
        String rootId = "";
        String rootTitle = "";
        String sessionTitle = "";
        String summary = "";
        String updatedAt = "";
        boolean replying = true;
        boolean awaiting = false;
        ReplyStatus status = ReplyStatus.REPLYING;

        ReplyState(String sessionKey, String rootId) {
            this.sessionKey = sessionKey;
            this.rootId = rootId == null ? "" : rootId;
            this.notificationId = notificationIdFor(this.rootId, sessionKey);
        }
    }
}
