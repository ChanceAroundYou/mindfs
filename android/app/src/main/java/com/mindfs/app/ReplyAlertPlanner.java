package com.mindfs.app;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * 纯 Java 状态机：把一帧 /api/replying-sessions 快照转成「要弹/要撤/要持久化」的计划。
 * 不依赖 Android，可 JVM 单测。
 */
final class ReplyAlertPlanner {
    static final class Snapshot {
        public String rootId = "";
        public String sessionKey = "";
        public String rootTitle = "";
        public String sessionTitle = "";
        public String summary = "";
        public String updatedAt = "";
        public boolean replying;
        public boolean awaiting;
    }

    static final class Alert {
        public String kind; // "done" | "awaiting"
        public String rootId = "";
        public String sessionKey = "";
        public String rootTitle = "";
        public String sessionTitle = "";
        public String summary = "";
    }

    static final class Plan {
        public final List<Snapshot> live = new ArrayList<>();
        public final List<Alert> post = new ArrayList<>();
        public final List<String> cancel = new ArrayList<>();
        public Set<String> awaitingNotified = new HashSet<>();
    }

    static String keyOf(String rootId, String sessionKey) {
        return (rootId == null ? "" : rootId) + "\u001f" + (sessionKey == null ? "" : sessionKey);
    }

    Plan apply(List<Snapshot> observed, Set<String> awaitingNotifiedIn) {
        Set<String> notified = new HashSet<>(awaitingNotifiedIn == null ? new HashSet<>() : awaitingNotifiedIn);
        Plan plan = new Plan();
        Set<String> seen = new HashSet<>();

        for (Snapshot s : observed) {
            if (s == null || s.sessionKey == null || s.sessionKey.isEmpty()) continue;
            String k = keyOf(s.rootId, s.sessionKey);
            seen.add(k);
            plan.live.add(s);

            if (s.awaiting) {
                if (!notified.contains(k)) {
                    Alert a = new Alert();
                    a.kind = "awaiting";
                    a.rootId = s.rootId; a.sessionKey = s.sessionKey;
                    a.rootTitle = s.rootTitle; a.sessionTitle = s.sessionTitle;
                    a.summary = s.summary;
                    plan.post.add(a);
                    notified.add(k);
                }
            } else {
                // 不再等待：撤销之前的 awaiting 提醒
                if (notified.contains(k)) {
                    plan.cancel.add(k);
                    notified.remove(k);
                }
            }
        }

        // 消失的会话：上一轮 replying 的 → DONE；只 awaiting 的 → 撤销
        for (String k : new HashSet<>(notified)) {
            // notified 里只含 awaiting；消失且 awaiting→撤销
            // 但 disappearing replying 的 DONE 由 Service 层根据 states 判断，这里无法知道上一轮 replying
            // 因此 disappearing 的 awaiting-only 撤销在下面统一处理：所有 notified 中不在 seen 的都撤销
            if (!seen.contains(k)) {
                plan.cancel.add(k);
                notified.remove(k);
            }
        }
        plan.awaitingNotified = notified;
        return plan;
    }
}
