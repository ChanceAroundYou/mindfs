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
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.TimeZone;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
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
    static final String EXTRA_NODES = "nodes";
    static final String EXTRA_VIEWING = "viewing";

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
    private static final String PREF_NODES = "nodes";
    private static final String PREF_STARTED_AT = "started_at";
    private static final String PREF_LAST_ALERT = "last_alert";
    private static final String PREF_VIEWING = "viewing";
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
    // 长轮询：服务端空等最长 20s（隔着反向代理，25s 会先被代理掐断），客户端读超时留 10s 余量。
    private static final int WAIT_TIMEOUT_SECONDS = 20;
    private static final int WAIT_READ_TIMEOUT_MS = 30_000;
    // 降级成普通轮询后的间隔；10 分钟后再试一次长轮询（旧服务端可能已经升级）。
    private static final long POLL_FALLBACK_INTERVAL_MS = 10_000;
    private static final long WAIT_RETRY_INTERVAL_MS = 600_000;
    // 未降级的偶发错误退避；连续 3 次才降级。
    private static final long ERROR_BACKOFF_MS = 5_000;
    // 节点的快照只在最近 90s 内成功过才算「可信」，否则不进 live、不参与「恰好 1 个」直达。
    private static final long NODE_FRESH_MS = 90_000;
    private static final int WAIT_FAILURES_BEFORE_FALLBACK = 3;
    // 失败路径重算 live 时用的「伪节点」id：applyNode 会按各节点自己的 lastSuccessAt
    // 重算全局新鲜度，但没有任何条目属于这个 id，所以不会误判完成。见 failNode()。
    private static final String FRESHNESS_REFRESH_NODE_ID = "\u0000freshness-refresh";
    private static final long[] ALERT_VIBRATION_PATTERN = new long[] {0, 160, 110, 160};

    // 已被 ReplyStateMerger 取代的旧状态表：只剩死代码（showReplyNotifications 等）引用，
    // 本次改动明确不动那批死代码，保留它们只为了不再放大 diff。
    private final Map<String, ReplyState> states = new HashMap<>();
    private final Map<Integer, String> notificationSignatures = new HashMap<>();
    private NotificationManager notificationManager;
    // 每个节点一条等待循环；线程池随 resume/stop 启停，nodeGeneration 让旧循环退出。
    private ScheduledExecutorService nodeExecutor;
    private volatile int nodeGeneration = 0;
    private String activeNodesSignature = "";
    // 合并 + DONE 判定 + 新鲜度全在 ReplyStateMerger 里；所有应用都串行化在 applyLock 下。
    private final Object applyLock = new Object();
    private ReplyStateMerger merger = new ReplyStateMerger(NODE_FRESH_MS);
    // 诊断用镜像（关于弹窗要能一眼看出哪个节点走 push、哪个降级到 poll）。
    private final Map<String, String> nodeModes = new ConcurrentHashMap<>();
    private final Map<String, String> nodeResults = new ConcurrentHashMap<>();
    private final Map<String, Long> nodeLastSuccessAt = new ConcurrentHashMap<>();
    // 停服时主动 disconnect，别让阻塞中的等待请求拖住线程直到读超时。
    private final Set<HttpURLConnection> openConnections =
        Collections.synchronizedSet(new HashSet<HttpURLConnection>());
    // 已投递的完成/提问提醒：合并器会把已完成条目剪掉，stopAll 只能靠这份清单撤销通知。
    private final Set<Integer> alertNotificationIds =
        Collections.synchronizedSet(new HashSet<Integer>());
    private volatile boolean running = false;
    private String apiBaseUrl = "";
    private String token = "";
    private boolean e2eeRequired = false;
    private String e2eeNodeId = "";
    private String e2eeClientId = "";
    private String e2eeTransportKey = "";
    private NotificationMode notificationMode = NotificationMode.NONE;
    // 节点表：[{id,url}]，url 是前端算好的整条 /api/replying-sessions 地址
    // （含节点路由、user= 账户参数、挂载前缀）——原生自己拼必然漏前缀。
    private String nodesJSON = "";
    // 用户当前正在看的会话（nodeId␟rootId␟sessionKey）；为空表示在后台/没在看。
    // 只抑制这一条，其余会话在前台也照常提醒。
    private String viewingKey = "";
    private static SSLSocketFactory localTrustAllSocketFactory;

    @Override
    public void onCreate() {
        super.onCreate();
        notificationManager = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
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
        running = false;
        stopNodeLoops();
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
        if (intent.hasExtra(EXTRA_NODES)) {
            nodesJSON = safe(intent.getStringExtra(EXTRA_NODES));
            editor.putString(PREF_NODES, nodesJSON);
        }
        if (intent.hasExtra(EXTRA_VIEWING)) {
            viewingKey = safe(intent.getStringExtra(EXTRA_VIEWING));
            editor.putString(PREF_VIEWING, viewingKey);
        }
        if (intent.hasExtra(EXTRA_E2EE_TRANSPORT_KEY)) {
            e2eeTransportKey = safe(intent.getStringExtra(EXTRA_E2EE_TRANSPORT_KEY));
            editor.putString(PREF_E2EE_TRANSPORT_KEY, e2eeTransportKey);
        }
        editor.apply();
        Log.i(TAG, "configured apiBaseUrl=" + apiBaseUrl + " e2eeRequired=" + e2eeRequired + " e2eeReady=" + hasE2EESession());
        // 节点表/兜底地址变了才重启循环：viewing 会频繁下发，按它重启会把 ETag 和
        // 正在进行的等待全部打掉。
        if (running && !nodesSignature().equals(activeNodesSignature)) {
            Log.i(TAG, "node targets changed, restarting reply loops");
            startNodeLoops();
        }
    }

    private void loadConfig() {
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        apiBaseUrl = safe(prefs.getString(PREF_API_BASE_URL, ""));
        token = safe(prefs.getString(PREF_TOKEN, ""));
        e2eeRequired = prefs.getBoolean(PREF_E2EE_REQUIRED, false);
        e2eeNodeId = safe(prefs.getString(PREF_E2EE_NODE_ID, ""));
        e2eeClientId = safe(prefs.getString(PREF_E2EE_CLIENT_ID, ""));
        e2eeTransportKey = safe(prefs.getString(PREF_E2EE_TRANSPORT_KEY, ""));
        nodesJSON = safe(prefs.getString(PREF_NODES, ""));
        viewingKey = safe(prefs.getString(PREF_VIEWING, ""));
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
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit()
            .putString(PREF_STARTED_AT, new SimpleDateFormat("MM-dd HH:mm:ss", Locale.US).format(new Date()))
            .apply();
        running = true;
        startForegroundCompat(FOREGROUND_ID, buildIdleNotification());
        startNodeLoops();
    }

    private void pausePolling() {
        Log.i(TAG, "pause polling");
        running = false;
        stopNodeLoops();
        stopForegroundCompat();
        stopSelf();
    }

    private void stopAll() {
        running = false;
        stopNodeLoops();
        List<ReplyStateMerger.Entry> tracked;
        synchronized (applyLock) {
            tracked = new ArrayList<>(merger.states().values());
            merger = new ReplyStateMerger(NODE_FRESH_MS);
        }
        for (ReplyStateMerger.Entry entry : tracked) {
            notificationManager.cancel(notificationIdFor(entry));
        }
        synchronized (alertNotificationIds) {
            for (Integer id : alertNotificationIds) {
                notificationManager.cancel(id);
            }
            alertNotificationIds.clear();
        }
        Set<String> notified = getSharedPreferences(PREFS_NAME, MODE_PRIVATE).getStringSet(PREF_AWAITING_NOTIFIED, new HashSet<>());
        for (String k : notified) {
            String[] parts = k.split("\u001f", 3);
            if (parts.length < 3) {
                continue;
            }
            notificationManager.cancel(notificationIdFor(parts[0], parts[1], parts[2]));
        }
        notificationManager.cancel(FOREGROUND_ID);
        notificationSignatures.clear();
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit().remove(PREF_AWAITING_NOTIFIED).apply();
        states.clear();
        nodeModes.clear();
        nodeResults.clear();
        nodeLastSuccessAt.clear();
        stopForegroundCompat();
        stopSelf();
    }

    // ── 每节点一条循环：长轮询为主，404/405 或连续失败降级成 10s 普通轮询 ─────────

    private synchronized void startNodeLoops() {
        stopNodeLoopsLocked();
        final int generation = ++nodeGeneration;
        List<NodeTarget> targets = nodeTargets();
        activeNodesSignature = signatureOf(targets);
        if (targets.isEmpty()) {
            recordPollDebug(false, 0, "没有配置任何节点");
            return;
        }
        ScheduledExecutorService pool = Executors.newScheduledThreadPool(Math.max(2, targets.size()));
        nodeExecutor = pool;
        for (final NodeTarget target : targets) {
            try {
                pool.execute(new Runnable() {
                    @Override
                    public void run() {
                        runNodeLoop(target, generation);
                    }
                });
            } catch (RuntimeException ex) {
                Log.w(TAG, "failed to start reply loop node=" + diagnosticKey(target), ex);
            }
        }
        Log.i(TAG, "started " + targets.size() + " reply loop(s) generation=" + generation);
    }

    private synchronized void stopNodeLoops() {
        stopNodeLoopsLocked();
    }

    private void stopNodeLoopsLocked() {
        nodeGeneration++;
        ScheduledExecutorService pool = nodeExecutor;
        nodeExecutor = null;
        if (pool != null) {
            pool.shutdownNow();
        }
        // 阻塞在读上的等待请求不会因为 interrupt 立刻返回：主动断开，暂停/停止才能马上收工。
        disconnectOpenConnections();
    }

    private void disconnectOpenConnections() {
        List<HttpURLConnection> pending;
        synchronized (openConnections) {
            pending = new ArrayList<>(openConnections);
            openConnections.clear();
        }
        for (HttpURLConnection conn : pending) {
            try {
                conn.disconnect();
            } catch (Exception ignored) {
                // 这里的断开失败无所谓：读写线程最多再等到读超时自行退出。
            }
        }
    }

    private String nodesSignature() {
        return signatureOf(nodeTargets());
    }

    private static String signatureOf(List<NodeTarget> targets) {
        StringBuilder builder = new StringBuilder();
        for (NodeTarget target : targets) {
            builder.append(target.id).append('\u001f')
                .append(target.url).append('\u001f')
                .append(target.waitUrl).append('\n');
        }
        return builder.toString();
    }

    private void runNodeLoop(NodeTarget target, int generation) {
        String etag = "";
        int failures = 0;
        // retryWaitAt：> now 表示这一轮走普通轮询，到点再试长轮询。MAGIC 上限 = 该节点没有长轮询端点。
        long retryWaitAt = target.waitUrl.isEmpty() ? Long.MAX_VALUE : 0L;
        if (target.waitUrl.isEmpty()) {
            setNodeMode(target, "poll", "长轮询不可用");
        }
        while (running && nodeGeneration == generation) {
            if (System.currentTimeMillis() >= retryWaitAt) {
                try {
                    // 第一次不带 If-None-Match：服务端立刻回全量快照，用来播种。
                    WaitResult result = fetchWait(target, etag);
                    if (result.status == 404 || result.status == 405) {
                        // 旧服务端：立刻退回轮询，不要先睡 10 分钟。
                        failures = 0;
                        retryWaitAt = System.currentTimeMillis() + WAIT_RETRY_INTERVAL_MS;
                        setNodeMode(target, "poll", "等待端点不可用（HTTP " + result.status + "）");
                        continue;
                    }
                    if (result.status != 200 && result.status != 304) {
                        throw new IllegalStateException("HTTP " + result.status);
                    }
                    failures = 0;
                    if (!result.etag.isEmpty()) {
                        etag = result.etag;
                    }
                    if (result.status == 200) {
                        applyNodeSnapshot(target, result.entries);
                        setNodeMode(target, "push", "等待返回 200");
                    } else {
                        // 304：快照没变，但要刷新节点新鲜度，否则空闲节点 90s 后会被判成掉线。
                        refreshNodeFreshness(target);
                        setNodeMode(target, "push", "等待返回 304");
                    }
                } catch (Exception ex) {
                    if (!isCurrentLoop(generation)) {
                        return;
                    }
                    failures++;
                    failNode(target, ex);
                    setNodeMode(target, "push", "等待失败：" + summarize(ex));
                    if (failures >= WAIT_FAILURES_BEFORE_FALLBACK) {
                        failures = 0;
                        retryWaitAt = System.currentTimeMillis() + WAIT_RETRY_INTERVAL_MS;
                        setNodeMode(target, "poll", "连续 " + WAIT_FAILURES_BEFORE_FALLBACK
                            + " 次等待失败，10 分钟后重试长轮询");
                        continue;
                    }
                    if (!sleepWhileRunning(ERROR_BACKOFF_MS, generation)) {
                        return;
                    }
                }
                continue;
            }

            // 普通轮询（降级路径 / 没有长轮询端点的旧配置）。
            if (!sleepWhileRunning(POLL_FALLBACK_INTERVAL_MS, generation)) {
                return;
            }
            try {
                applyNodeSnapshot(target, fetchReplyingSessions(target));
                failures = 0;
                setNodeMode(target, "poll", "轮询正常");
            } catch (Exception ex) {
                if (!isCurrentLoop(generation)) {
                    return;
                }
                failures++;
                failNode(target, ex);
                setNodeMode(target, "poll", "轮询失败：" + summarize(ex));
                if (!sleepWhileRunning(ERROR_BACKOFF_MS, generation)) {
                    return;
                }
            }
        }
        Log.i(TAG, "reply loop stopped node=" + diagnosticKey(target) + " generation=" + generation);
    }

    private boolean isCurrentLoop(int generation) {
        return running && nodeGeneration == generation;
    }

    private boolean sleepWhileRunning(long millis, int generation) {
        long deadline = System.currentTimeMillis() + millis;
        while (isCurrentLoop(generation)) {
            long remaining = deadline - System.currentTimeMillis();
            if (remaining <= 0) {
                return true;
            }
            try {
                Thread.sleep(Math.min(remaining, 1000L));
            } catch (InterruptedException ex) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        return false;
    }

    private static final class NodeTarget {
        final String id;
        final String url;
        final String waitUrl;
        NodeTarget(String id, String url, String waitUrl) {
            this.id = id == null ? "" : id;
            this.url = url == null ? "" : url;
            this.waitUrl = waitUrl == null ? "" : waitUrl;
        }
    }

    private static final class WaitResult {
        final int status;
        final String etag;
        final Map<String, ReplyStateMerger.Entry> entries;
        WaitResult(int status, String etag, Map<String, ReplyStateMerger.Entry> entries) {
            this.status = status;
            this.etag = etag == null ? "" : etag;
            this.entries = entries == null ? Collections.<String, ReplyStateMerger.Entry>emptyMap() : entries;
        }
    }

    private static String resolveWaitUrl(String url, String explicit) {
        String wait = safe(explicit);
        if (!wait.isEmpty()) {
            return wait;
        }
        // 老配置/单节点兜底没带 waitUrl：只有确认 url 就是普通端点时才敢拼（带查询串时拼出来是错的）。
        if (safe(url).endsWith("/api/replying-sessions")) {
            return url + "/wait";
        }
        return "";
    }

    private static String withWaitTimeout(String urlString) {
        // waitUrl 可能已经带 user= 账户参数，第二个参数得用 &。
        if (urlString.matches(".*[?&]timeout=.*")) {
            return urlString;
        }
        return urlString + (urlString.contains("?") ? "&" : "?") + "timeout=" + WAIT_TIMEOUT_SECONDS;
    }

    // 节点表由前端推送（每项是算好的完整 URL）。没推过时退回单节点 apiBaseUrl，
    // 兼容升级前的配置。
    private List<NodeTarget> nodeTargets() {
        List<NodeTarget> out = new ArrayList<>();
        try {
            JSONArray arr = new JSONArray(nodesJSON);
            for (int i = 0; i < arr.length(); i++) {
                JSONObject node = arr.optJSONObject(i);
                if (node == null) {
                    continue;
                }
                String url = safe(node.optString("url", ""));
                if (url.isEmpty()) {
                    continue;
                }
                out.add(new NodeTarget(
                    safe(node.optString("id", "")),
                    url,
                    resolveWaitUrl(url, node.optString("waitUrl", ""))));
            }
        } catch (Exception ignored) {
            // nodesJSON 为空或格式不对：走下面的单节点兜底。
        }
        if (out.isEmpty() && !apiBaseUrl.isEmpty()) {
            String url = apiBaseUrl + "/api/replying-sessions";
            out.add(new NodeTarget("", url, url + "/wait"));
        }
        return out;
    }

    static String compositeKey(String nodeId, String rootId, String sessionKey) {
        return safe(nodeId) + "\u001f" + safe(rootId) + "\u001f" + safe(sessionKey);
    }

    private static String diagnosticKey(NodeTarget target) {
        return target.id.isEmpty() ? "default" : target.id;
    }

    private void setNodeMode(NodeTarget target, String mode, String result) {
        String key = diagnosticKey(target);
        nodeModes.put(key, mode);
        nodeResults.put(key, result);
    }

    private int freshNodeCount() {
        long now = System.currentTimeMillis();
        int fresh = 0;
        for (Long lastSuccess : nodeLastSuccessAt.values()) {
            if (lastSuccess != null && now - lastSuccess <= NODE_FRESH_MS) {
                fresh++;
            }
        }
        return fresh;
    }

    private static String summarize(Exception ex) {
        if (ex == null) {
            return "";
        }
        String message = ex.getMessage();
        String type = ex.getClass().getSimpleName();
        return message == null || message.isEmpty() ? type : type + ": " + message;
    }

    // 抽出连接构造（token / E2EE / proof / 本地自签 TLS），等待与普通轮询共用。
    private HttpURLConnection openConnection(NodeTarget target, String urlString) throws Exception {
        URL url = new URL(urlString);
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
        openConnections.add(conn);
        return conn;
    }

    // 一次长轮询。返回 200（新快照）/304（未变）/404/405（端点不存在）；其他状态码抛异常走失败路径。
    private WaitResult fetchWait(NodeTarget target, String etag) throws Exception {
        HttpURLConnection conn = openConnection(target, withWaitTimeout(target.waitUrl));
        try {
            // 服务端最多等 timeout，客户端晚 10s 才判超时，避免把正常的空等当成失败。
            conn.setReadTimeout(WAIT_READ_TIMEOUT_MS);
            if (etag != null && !etag.isEmpty()) {
                conn.setRequestProperty("If-None-Match", etag);
            }
            int status = conn.getResponseCode();
            String responseEtag = safe(conn.getHeaderField("ETag"));
            if (status == HttpURLConnection.HTTP_NOT_MODIFIED) {
                return new WaitResult(status, responseEtag, null);
            }
            if (status == HttpURLConnection.HTTP_NOT_FOUND || status == HttpURLConnection.HTTP_BAD_METHOD) {
                return new WaitResult(status, "", null);
            }
            InputStream stream = status >= 200 && status < 300 ? conn.getInputStream() : conn.getErrorStream();
            String body = readAll(stream);
            boolean protectedResponse = "1".equals(safe(conn.getHeaderField(E2EE_HEADER)));
            if (status < 200 || status >= 300) {
                if (status == 401 && e2eeRequired) {
                    clearE2EESession();
                }
                throw new IllegalStateException("HTTP " + status + ": " + body);
            }
            if (protectedResponse) {
                body = decryptProtectedBody(body);
            }
            return new WaitResult(status, responseEtag, parseReplyingSessions(target, body));
        } finally {
            openConnections.remove(conn);
            conn.disconnect();
        }
    }

    private Map<String, ReplyStateMerger.Entry> fetchReplyingSessions(NodeTarget target) throws Exception {
        HttpURLConnection conn = openConnection(target, target.url);
        try {
            int status = conn.getResponseCode();
            InputStream stream = status >= 200 && status < 300 ? conn.getInputStream() : conn.getErrorStream();
            String body = readAll(stream);
            boolean protectedResponse = "1".equals(safe(conn.getHeaderField(E2EE_HEADER)));
            if (status < 200 || status >= 300) {
                if (status == 401 && e2eeRequired) {
                    clearE2EESession();
                }
                throw new IllegalStateException("HTTP " + status + ": " + body);
            }
            if (protectedResponse) {
                body = decryptProtectedBody(body);
            }
            return parseReplyingSessions(target, body);
        } finally {
            openConnections.remove(conn);
            conn.disconnect();
        }
    }

    private Map<String, ReplyStateMerger.Entry> parseReplyingSessions(NodeTarget target, String body) throws Exception {
        JSONObject root = new JSONObject(body);
        JSONArray sessions = root.optJSONArray("sessions");
        Map<String, ReplyStateMerger.Entry> next = new LinkedHashMap<>();
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
            // nodeId 必须与 applyNode(nodeId, ...) 收到的完全一致，否则这个节点的快照清不掉这些行。
            ReplyStateMerger.Entry entry = new ReplyStateMerger.Entry(target.id, rootId, sessionKey);
            entry.rootTitle = safe(item.optString("rootTitle", ""));
            entry.sessionTitle = safe(item.optString("sessionTitle", ""));
            entry.summary = safe(item.optString("summary", ""));
            entry.updatedAt = safe(item.optString("updatedAt", ""));
            String st = safe(item.optString("status", ""));
            entry.replying = "replying".equalsIgnoreCase(st) || item.optBoolean("active", false);
            entry.awaiting = item.optBoolean("awaiting_answer", false) || item.optBoolean("awaitingAnswer", false);
            next.put(entry.key(), entry);
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

    // 把一个节点的权威快照应用到合并视图：DONE 判定、awaiting 提醒、常驻通知都在这里。
    // 网络请求在外层完成；applyLock 只保护「应用」这一段，绝不持锁做 I/O。
    private void applyNodeSnapshot(NodeTarget target, Map<String, ReplyStateMerger.Entry> entries) {
        if (!running) {
            return;
        }
        List<ReplyStateMerger.Entry> snapshot = new ArrayList<>();
        if (entries != null) {
            snapshot.addAll(entries.values());
        }
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        Set<String> awaitingNotified = new HashSet<>(
            prefs.getStringSet(PREF_AWAITING_NOTIFIED, new HashSet<String>()));
        synchronized (applyLock) {
            if (!running) {
                return;
            }
            long now = System.currentTimeMillis();
            ReplyStateMerger.Result result = merger.applyNode(target.id, snapshot, now);
            nodeLastSuccessAt.put(diagnosticKey(target), now);

            // 1. DONE 检测：merger 只对本轮成功返回的节点判定 —— 节点离线时缺席不代表任务结束。
            for (ReplyStateMerger.Entry entry : result.newlyCompleted) {
                postAlert("done", entry);
                vibrateReplyCompletion();
            }

            // 2. awaiting 生命周期：只看新鲜的合并视图（不可信节点的会话不提醒）。
            //    planner 的键是 nodeId␟rootId，复合后与 Entry.key() 一致。
            Map<String, ReplyStateMerger.Entry> all = merger.states();
            List<ReplyAlertPlanner.Snapshot> snaps = new ArrayList<>();
            for (String key : result.currentKeys) {
                ReplyStateMerger.Entry entry = all.get(key);
                if (entry == null) {
                    continue;
                }
                ReplyAlertPlanner.Snapshot sn = new ReplyAlertPlanner.Snapshot();
                sn.rootId = safe(entry.nodeId) + "\u001f" + entry.rootId;
                sn.sessionKey = entry.sessionKey;
                sn.rootTitle = entry.rootTitle;
                sn.sessionTitle = entry.sessionTitle;
                sn.summary = entry.summary;
                sn.updatedAt = entry.updatedAt;
                sn.replying = entry.replying;
                sn.awaiting = entry.awaiting;
                snaps.add(sn);
            }
            ReplyAlertPlanner planner = new ReplyAlertPlanner();
            ReplyAlertPlanner.Plan plan = planner.apply(snaps, awaitingNotified);
            for (ReplyAlertPlanner.Alert alert : plan.post) {
                ReplyStateMerger.Entry entry = all.get(safe(alert.rootId) + "\u001f" + alert.sessionKey);
                if (entry != null) {
                    postAlert(alert.kind, entry);
                    vibrateReplyCompletion();
                }
            }
            for (String key : plan.cancel) {
                String[] parts = key.split("\u001f", 3);
                if (parts.length < 3) {
                    continue;
                }
                notificationManager.cancel(notificationIdFor(parts[0], parts[1], parts[2]));
            }
            prefs.edit().putStringSet(PREF_AWAITING_NOTIFIED, plan.awaitingNotified).apply();

            // 3. 常驻前台服务通知：只说「有几个在跑」，不点名。
            updateForegroundNotification(result.live);
            recordPollDebug(true, all.size(), "");
        }
    }

    // 304：快照内容没变，但这也是「节点还活着」的证据 —— 原样回填一次，刷新新鲜度。
    private void refreshNodeFreshness(NodeTarget target) {
        if (!running) {
            return;
        }
        synchronized (applyLock) {
            if (!running) {
                return;
            }
            List<ReplyStateMerger.Entry> current = new ArrayList<>();
            for (ReplyStateMerger.Entry entry : merger.states().values()) {
                if (target.id.equals(entry.nodeId)) {
                    current.add(entry);
                }
            }
            long now = System.currentTimeMillis();
            ReplyStateMerger.Result result = merger.applyNode(target.id, current, now);
            nodeLastSuccessAt.put(diagnosticKey(target), now);
            updateForegroundNotification(result.live);
            recordPollDebug(true, merger.states().size(), "");
        }
    }

    // 节点失败：保留它的条目（离线不等于任务结束），但从 live 里摘掉并刷新常驻通知，
    // 否则节点掉线后常驻通知会永远显示「有会话正在回复」。
    private void failNode(NodeTarget target, Exception ex) {
        Log.w(TAG, "reply wait failed node=" + diagnosticKey(target), ex);
        synchronized (applyLock) {
            if (!running) {
                return;
            }
            merger.markNodeFailed(target.id);
            nodeLastSuccessAt.remove(diagnosticKey(target));
            // applyNode 会按各节点自己的 lastSuccessAt 重算全局 live；伪节点没有条目，
            // 所以 DONE 判定不会命中任何会话。
            ReplyStateMerger.Result result = merger.applyNode(
                FRESHNESS_REFRESH_NODE_ID,
                Collections.<ReplyStateMerger.Entry>emptyList(),
                System.currentTimeMillis());
            updateForegroundNotification(result.live);
            recordPollDebug(freshNodeCount() > 0, merger.states().size(),
                "节点 " + diagnosticKey(target) + " 失败：" + summarize(ex));
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
            o.put("nodes", nodeTargets().size());
            // 每个节点走的是长轮询（push）还是降级的普通轮询（poll），以及最近一次结果。
            JSONObject modes = new JSONObject();
            for (Map.Entry<String, String> item : nodeModes.entrySet()) {
                modes.put(item.getKey(), item.getValue());
            }
            o.put("modes", modes);
            JSONObject results = new JSONObject();
            for (Map.Entry<String, String> item : nodeResults.entrySet()) {
                results.put(item.getKey(), item.getValue());
            }
            o.put("results", results);
            o.put("freshNodes", freshNodeCount());
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

    // 「有没有发出去」「是不是被正在看抑制了」必须能事后核对，否则只能靠猜。
    private void recordAlert(String kind, ReplyState state, boolean suppressed) {
        recordAlert(kind, state.sessionTitle, suppressed);
    }

    private void recordAlert(String kind, ReplyStateMerger.Entry state, boolean suppressed) {
        recordAlert(kind, state.sessionTitle, suppressed);
    }

    private void recordAlert(String kind, String sessionTitle, boolean suppressed) {
        String line = (suppressed ? "已抑制（正在看）" : "已发送")
            + " " + kind + " · " + sessionTitle
            + " · " + new SimpleDateFormat("MM-dd HH:mm:ss", Locale.US).format(new Date());
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE).edit()
            .putString(PREF_LAST_ALERT, line)
            .apply();
    }

    static String readPollDebug(Context context) {
        return context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE).getString(PREF_DEBUG, "");
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
        SharedPreferences prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
        String startedAt = prefs.getString(PREF_STARTED_AT, "");
        String lastAlert = prefs.getString(PREF_LAST_ALERT, "");
        sb.append(" · 轮询始于=").append(startedAt.isEmpty() ? "未启动" : startedAt);
        sb.append(" · 模式=").append(pollModeSummary(prefs.getString(PREF_DEBUG, "")));
        sb.append(" · 最近提醒=").append(lastAlert.isEmpty() ? "无" : lastAlert);
        return sb.toString();
    }

    // 诊断串里的「模式=push 3 / poll 1」：数最近一次落库的每节点模式。
    private static String pollModeSummary(String debugJSON) {
        int push = 0;
        int poll = 0;
        try {
            JSONObject debug = new JSONObject(safe(debugJSON));
            JSONObject modes = debug.optJSONObject("modes");
            if (modes != null) {
                Iterator<String> keys = modes.keys();
                while (keys.hasNext()) {
                    String mode = safe(modes.optString(keys.next(), ""));
                    if ("push".equals(mode)) {
                        push++;
                    } else if ("poll".equals(mode)) {
                        poll++;
                    }
                }
            }
        } catch (Exception ignored) {
            // 没有调试记录或格式不对：按 0 显示，不影响其他诊断项。
        }
        return "push " + push + " / poll " + poll;
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
        String error = showTestNotification(context, 799999, "MindFS 测试提醒", "通知链路正常。");
        if (!error.isEmpty()) {
            return error;
        }
        // 前台发通知时，很多 ROM（MIUI 等）会吞掉横幅，通知只在通知栏里躺着 ——
        // 那不代表后台也收不到。10 秒后再发一条，用户这时已切到后台，
        // 正好用来验证真正的提醒场景。
        final Context appContext = context.getApplicationContext();
        new android.os.Handler(android.os.Looper.getMainLooper()).postDelayed(() -> {
            ensureChannels(appContext);
            showTestNotification(appContext, 799998, "MindFS 后台提醒测试", "这条是在你切到后台之后发出的。");
        }, 10000);
        return "";
    }

    private static String showTestNotification(Context context, int id, String title, String text) {
        NotificationManager nm = (NotificationManager) context.getSystemService(Context.NOTIFICATION_SERVICE);
        if (nm == null) {
            return "系统通知服务不可用";
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
                context, id, launch,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE));
        }
        b.setSmallIcon(R.drawable.ic_stat_mindfs_notification)
         .setContentTitle(title)
         .setContentText(text)
         .setAutoCancel(true)
         .setCategory(Notification.CATEGORY_STATUS);
        try {
            nm.notify(id, b.build());
        } catch (Exception ex) {
            return "通知投递失败：" + ex.getMessage();
        }
        return "";
    }

    private void postAlert(String kind, ReplyStateMerger.Entry state) {
        String stateKey = state.key();
        recordAlert(kind, state, viewingKey.equals(stateKey) && !viewingKey.isEmpty());
        // 正在看这条会话就不打扰（JS 只在 App 可见时上报 viewing）。
        if (!viewingKey.isEmpty() && viewingKey.equals(stateKey)) {
            return;
        }
        String title = buildReplyTitlePrefix(state) + " · " + ("awaiting".equals(kind) ? "需要你回答" : "完成");
        String text = state.summary;
        Intent launchIntent = getPackageManager().getLaunchIntentForPackage(getPackageName());
        if (launchIntent == null) launchIntent = new Intent(this, MainActivity.class);
        launchIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        launchIntent.putExtra("nodeId", state.nodeId);
        launchIntent.putExtra("rootId", state.rootId);
        launchIntent.putExtra("sessionKey", state.sessionKey);
        int id = notificationIdFor(state);
        alertNotificationIds.add(id);
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

    private void updateForegroundNotification(List<ReplyStateMerger.Entry> live) {
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

    private String buildReplyTitlePrefix(ReplyStateMerger.Entry state) {
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

    // 每会话一条通知：id 必须把节点也算进去，否则两台机器上同名 root/session 会互相覆盖。
    static int notificationIdFor(String nodeId, String rootId, String sessionKey) {
        return NOTIFICATION_BASE_ID + Math.abs(compositeKey(nodeId, rootId, sessionKey).hashCode() % 100000);
    }

    private static int notificationIdFor(ReplyStateMerger.Entry entry) {
        return notificationIdFor(entry.nodeId, entry.rootId, entry.sessionKey);
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
        final String key;        // nodeId␟rootId␟sessionKey，跨节点的唯一身份
        final String nodeId;
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

        ReplyState(String key, String nodeId, String rootId, String sessionKey) {
            this.key = key;
            this.nodeId = nodeId == null ? "" : nodeId;
            this.rootId = rootId == null ? "" : rootId;
            this.sessionKey = sessionKey == null ? "" : sessionKey;
            this.notificationId = notificationIdFor(this.nodeId, this.rootId, this.sessionKey);
        }
    }
}
