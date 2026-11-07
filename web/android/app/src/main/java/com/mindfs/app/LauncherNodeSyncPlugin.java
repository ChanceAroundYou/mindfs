package com.mindfs.app;

import android.content.Context;
import android.content.SharedPreferences;
import com.getcapacitor.JSArray;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

@CapacitorPlugin(name = "LauncherNodeSync")
public class LauncherNodeSyncPlugin extends Plugin {
    private static final String PREFS_NAME = "mindfs_launcher_node_sync";
    private static final String KEY_LAUNCHER_NODES = "launcher_nodes";

    @PluginMethod
    public void getLauncherNodes(PluginCall call) {
        JSArray nodes = parseNodesJSON(prefs(getContext()).getString(KEY_LAUNCHER_NODES, ""));

        JSObject result = new JSObject();
        result.put("nodes", nodes);
        result.put("count", nodes.length());
        call.resolve(result);
    }

    @PluginMethod
    public void setLauncherNodes(PluginCall call) {
        JSArray nodes = call.getArray("nodes");
        if (nodes == null) {
            call.reject("nodes is required");
            return;
        }

        prefs(getContext())
            .edit()
            .putString(KEY_LAUNCHER_NODES, nodes.toString())
            .apply();

        JSObject result = new JSObject();
        result.put("stored", true);
        result.put("count", nodes.length());
        call.resolve(result);
    }

    private static SharedPreferences prefs(Context context) {
        return context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
    }

    private static JSArray parseNodesJSON(String raw) {
        if (raw != null && !raw.trim().isEmpty()) {
            try {
                return new JSArray(raw);
            } catch (Exception ignored) {
            }
        }
        return new JSArray();
    }
}
