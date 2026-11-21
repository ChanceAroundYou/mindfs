package com.mindfs.app;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Pure-Java merge and DONE-detection logic for the background reply poller.
 *
 * <p>Each node runs its own long-poll loop, so every {@link #applyNode} call carries one node's
 * authoritative snapshot. The merger keeps per-node state, reports sessions that stopped
 * reporting on that node exactly once, and prunes them so a later reappearance is a new entry.</p>
 *
 * <p>Stale nodes (no successful snapshot within the freshness window) and nodes marked failed
 * keep their entries in {@link #states()} but never contribute to {@code live}, so a recovery
 * does not look like a fresh session and does not re-report an old completion.</p>
 *
 * <p>No Android imports: this class is unit-tested on the JVM.</p>
 */
final class ReplyStateMerger {
    private static final String KEY_SEPARATOR = "\u001f";

    /** One replying or awaiting session as reported by a single node. */
    static final class Entry {
        final String nodeId;
        final String rootId;
        final String sessionKey;
        String rootTitle = "";
        String sessionTitle = "";
        String summary = "";
        String updatedAt = "";
        boolean replying = true;
        boolean awaiting = false;
        /**
         * 子会话（Claude Code Task 子代理 / 看板节点 attach）。父会话那一轮还在跑，
         * 子任务回来的那一下不该弹提醒 —— 用户要的是「活干完了」。
         */
        boolean subSession = false;
        /**
         * 任务内部的一步（工作流节点回合 / 看板内部续轮）。与 subSession 一样只跳过
         * **完成提醒**，不影响「正在回复」指示灯与 awaiting 提醒。
         */
        boolean silent = false;

        Entry(String nodeId, String rootId, String sessionKey) {
            this.nodeId = safe(nodeId);
            this.rootId = safe(rootId);
            this.sessionKey = safe(sessionKey);
        }

        String key() {
            return nodeId + KEY_SEPARATOR + rootId + KEY_SEPARATOR + sessionKey;
        }
    }

    /** Outcome of one {@link #applyNode} call. */
    static final class Result {
        /** Entries that stopped replying on this node, carrying their last known fields. */
        final List<Entry> newlyCompleted = new ArrayList<>();
        /** Replying or awaiting entries that belong to fresh nodes. */
        final List<Entry> live = new ArrayList<>();
        /** Every key still present on a fresh node. */
        final Set<String> currentKeys = new HashSet<>();
    }

    private final long freshnessMillis;
    private final Map<String, Entry> states = new HashMap<>();
    private final Map<String, Long> lastSuccessAt = new HashMap<>();

    ReplyStateMerger(long freshnessMillis) {
        this.freshnessMillis = Math.max(0L, freshnessMillis);
    }

    /**
     * Replaces this node's entries with {@code entries}, diffs the previous snapshot for DONE
     * detection (this node only) and recomputes the live set across all fresh nodes.
     *
     * @param nodeId      node that produced the snapshot; a null or empty list means the node
     *                    currently has no replying sessions and completes whatever it had
     * @param entries     entries reported by that node; each entry's {@code nodeId} must match
     * @param nowMillis   caller-supplied clock, used for the freshness window
     */
    synchronized Result applyNode(String nodeId, List<Entry> entries, long nowMillis) {
        String node = safe(nodeId);
        Map<String, Entry> incoming = new LinkedHashMap<>();
        if (entries != null) {
            for (Entry entry : entries) {
                if (entry == null) {
                    continue;
                }
                incoming.put(entry.key(), entry);
            }
        }

        for (Map.Entry<String, Entry> item : incoming.entrySet()) {
            Entry entry = item.getValue();
            Entry existing = states.get(item.getKey());
            if (existing != null && entry.summary.isEmpty()) {
                // Keep the last known summary so the completion alert can still show it.
                entry.summary = existing.summary;
            }
            states.put(item.getKey(), entry);
        }

        // DONE detection is per node: this node's snapshot is authoritative for its sessions,
        // even when another node still lists the same sessionKey.
        Result result = new Result();
        List<String> gone = new ArrayList<>();
        for (Entry state : states.values()) {
            if (!node.equals(state.nodeId) || incoming.containsKey(state.key())) {
                continue;
            }
            gone.add(state.key());
            if (state.replying) {
                result.newlyCompleted.add(state);
            }
        }
        for (String key : gone) {
            states.remove(key);
        }

        lastSuccessAt.put(node, nowMillis);
        fillFresh(result, nowMillis);
        return result;
    }

    /** Marks a node as failed for freshness purposes until its next snapshot succeeds. */
    synchronized void markNodeFailed(String nodeId) {
        lastSuccessAt.remove(safe(nodeId));
    }

    /** Returns a copy: later {@link #applyNode} calls are not visible through this map. */
    synchronized Map<String, Entry> states() {
        return new LinkedHashMap<>(states);
    }

    private void fillFresh(Result result, long nowMillis) {
        for (Entry state : states.values()) {
            if (!isFresh(state.nodeId, nowMillis)) {
                continue;
            }
            result.currentKeys.add(state.key());
            if (state.replying || state.awaiting) {
                result.live.add(state);
            }
        }
    }

    private boolean isFresh(String nodeId, long nowMillis) {
        Long lastSuccess = lastSuccessAt.get(nodeId);
        return lastSuccess != null && nowMillis - lastSuccess <= freshnessMillis;
    }

    private static String safe(String value) {
        return value == null ? "" : value.trim();
    }
}