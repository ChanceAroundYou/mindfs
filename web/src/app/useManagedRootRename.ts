import { useCallback } from "react";
import { type ManagedRootPayload, mapManagedRootsToEntries } from "./appMisc";
import { setRootNodeMap } from "../services/net/rootNode";
import type { UseManagedRootsParams } from "./useManagedRootsTypes";

type Params = Pick<UseManagedRootsParams,
  | "managedRootByIdRef"
  | "managedRootIdsRef"
  | "setRootEntries"
  | "boundSessionByRootRef"
  | "suppressedAutoBindSessionByRootRef"
  | "drawerSessionByRootRef"
  | "selectedSessionByRootRef"
  | "drawerOpenByRootRef"
  | "pluginsLoadedByRootRef"
  | "pluginsLoadingByRootRef"
  | "setGitStatusExpandedByRoot"
  | "setGitHistoryExpandedByRoot"
  | "sessionCacheRef"
  | "loadedSessionRef"
  | "staleSessionKeysRef"
  | "entriesByPathRef"
  | "setEntriesByPath"
  | "invalidTreeCacheKeysRef"
  | "setManagedRootIds"
>;

export function useManagedRootRename(params: Params) {
  const {
    managedRootByIdRef, managedRootIdsRef, setRootEntries,
    boundSessionByRootRef, suppressedAutoBindSessionByRootRef,
    drawerSessionByRootRef, selectedSessionByRootRef, drawerOpenByRootRef,
    pluginsLoadedByRootRef, pluginsLoadingByRootRef,
    setGitStatusExpandedByRoot, setGitHistoryExpandedByRoot,
    sessionCacheRef, loadedSessionRef, staleSessionKeysRef,
    entriesByPathRef, setEntriesByPath, invalidTreeCacheKeysRef,
    setManagedRootIds,
  } = params;
  const applyManagedRootRename = useCallback(
    (oldRootID: string, rootPayload: ManagedRootPayload | null | undefined) => {
      const oldID = String(oldRootID || "").trim();
      const nextID = String(rootPayload?.id || "").trim();
      if (!oldID || !nextID) {
        return false;
      }

      const previousRoot =
        managedRootByIdRef.current[oldID] ||
        managedRootByIdRef.current[nextID] ||
        ({} as ManagedRootPayload);
      const nextRoot = {
        ...previousRoot,
        ...rootPayload,
        id: nextID,
      } as ManagedRootPayload;

      const nextRootById = { ...managedRootByIdRef.current };
      delete nextRootById[oldID];
      nextRootById[nextID] = nextRoot;
      managedRootByIdRef.current = nextRootById;
      setRootNodeMap(nextRootById as Record<string, any>);

      const moveRecordKey = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return;
        }
        for (const k of Object.keys(record)) {
          const idx = k.lastIndexOf("::");
          const rid = idx >= 0 ? k.slice(idx + 2) : k;
          if (rid !== oldID) {
            continue;
          }
          const nextKey = idx >= 0 ? `${k.slice(0, idx + 2)}${nextID}` : nextID;
          record[nextKey] = record[k];
          delete record[k];
        }
      };
      moveRecordKey(boundSessionByRootRef.current);
      moveRecordKey(suppressedAutoBindSessionByRootRef.current);
      moveRecordKey(drawerSessionByRootRef.current);
      moveRecordKey(selectedSessionByRootRef.current);
      moveRecordKey(drawerOpenByRootRef.current);
      moveRecordKey(pluginsLoadedByRootRef.current);
      moveRecordKey(pluginsLoadingByRootRef.current);

      const moveStateRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return record;
        }
        const next: Record<string, T> = {};
        for (const [k, v] of Object.entries(record)) {
          const idx = k.lastIndexOf("::");
          const rid = idx >= 0 ? k.slice(idx + 2) : k;
          const nextKey = idx >= 0 ? `${k.slice(0, idx + 2)}${nextID}` : nextID;
          next[rid === oldID ? nextKey : k] = v;
        }
        return next;
      };
      setGitStatusExpandedByRoot((prev) => moveStateRecord(prev));
      setGitHistoryExpandedByRoot((prev) => moveStateRecord(prev));

      const remapSessionCacheKey = (key: string): string | null => {
        const parts = key.split("::");
        if (parts.length === 3) {
          if (parts[1] !== oldID) return null;
          return `${parts[0]}::${nextID}::${parts.slice(2).join("::")}`;
        }
        if (parts.length === 2) {
          if (parts[0] !== oldID) return null;
          return `${nextID}::${parts[1]}`;
        }
        return null;
      };
      const moveCacheRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return;
        }
        for (const key of Object.keys(record)) {
          const nextKey = remapSessionCacheKey(key);
          if (nextKey === null) continue;
          record[nextKey] = record[key];
          delete record[key];
        }
      };
      moveCacheRecord(sessionCacheRef.current);
      moveCacheRecord(loadedSessionRef.current);
      if (oldID !== nextID) {
        staleSessionKeysRef.current = new Set(
          Array.from(staleSessionKeysRef.current)
            .map((key) => remapSessionCacheKey(key) ?? key),
        );
      }

      const moveTreeRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return record;
        }
        const next = { ...record };
        for (const key of Object.keys(record)) {
          const afterScope = key.includes("::")
            ? key.slice(key.lastIndexOf("::") + 2)
            : key;
          const idx = afterScope.indexOf(":");
          const rid = idx >= 0 ? afterScope.slice(0, idx) : afterScope;
          if (rid !== oldID) {
            continue;
          }
          const head = key.slice(0, key.length - afterScope.length);
          const nextAfter = `${nextID}${idx >= 0 ? afterScope.slice(idx) : ""}`;
          next[`${head}${nextAfter}`] = record[key];
          delete next[key];
        }
        return next;
      };
      entriesByPathRef.current = moveTreeRecord(entriesByPathRef.current);
      setEntriesByPath((prev) => moveTreeRecord(prev));
      if (oldID !== nextID) {
        invalidTreeCacheKeysRef.current = new Set(
          Array.from(invalidTreeCacheKeysRef.current).map((key) => {
            const afterScope = key.includes("::")
              ? key.slice(key.lastIndexOf("::") + 2)
              : key;
            const idx = afterScope.indexOf(":");
            const rid = idx >= 0 ? afterScope.slice(0, idx) : afterScope;
            if (rid !== oldID) return key;
            const head = key.slice(0, key.length - afterScope.length);
            return `${head}${nextID}${idx >= 0 ? afterScope.slice(idx) : ""}`;
          }),
        );
      }

      setManagedRootIds((prev) => {
        const source = prev.length ? prev : Array.from(managedRootIdsRef.current);
        let replaced = false;
        const nextIds = source.map((id) => {
          if (id !== oldID) {
            return id;
          }
          replaced = true;
          return nextID;
        });
        if (!replaced && !nextIds.includes(nextID)) {
          nextIds.push(nextID);
        }
        const deduped = Array.from(new Set(nextIds.filter(Boolean)));
        managedRootIdsRef.current = new Set(deduped);
        setRootEntries(
          mapManagedRootsToEntries(
            deduped
              .map((id) => managedRootByIdRef.current[id])
              .filter((dir): dir is ManagedRootPayload => !!dir),
          ),
        );
        return deduped;
      });
      return true;
    },
    [],
  );

  return { applyManagedRootRename };
}
