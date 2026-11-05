package com.mindfs.app;

import android.app.DownloadManager;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.MediaStore;
import android.text.TextUtils;
import android.util.Base64;
import android.webkit.CookieManager;
import android.webkit.URLUtil;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import androidx.core.content.FileProvider;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;

@CapacitorPlugin(name = "NativeDownload")
public class NativeDownloadPlugin extends Plugin {
    // 应用自身更新：下载 GitHub Release 里的 APK 并直接拉起系统安装器。
    // 用同步返回的 bridge 形态（与 NativeDownloadBridge.download 一致）：
    // 返回空串 = 已开始下载安装，非空 = 给用户看的错误/提示文案。
    public static String downloadAndInstallApk(Context context, String url, String filename) {
        if (TextUtils.isEmpty(url)) {
            return "缺少下载地址";
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            && !context.getPackageManager().canRequestPackageInstalls()) {
            try {
                Intent settings = new Intent(
                    android.provider.Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:" + context.getPackageName())
                );
                settings.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                context.startActivity(settings);
            } catch (Exception ignored) {
                // 打不开设置页时仍把提示给用户，让他手动去系统设置授权。
            }
            return "请先允许本应用安装未知应用，授权后再次点击检查更新";
        }
        String safeFilename = sanitizeFilename(filename);
        if (TextUtils.isEmpty(safeFilename) || !safeFilename.toLowerCase().endsWith(".apk")) {
            safeFilename = "mindfs-update.apk";
        }
        final String targetName = safeFilename;
        final Context appContext = context.getApplicationContext();
        new Thread(() -> {
            try {
                File dir = new File(appContext.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS), "updates");
                if (!dir.exists() && !dir.mkdirs()) {
                    throw new IllegalStateException("cannot create update dir");
                }
                File out = new File(dir, targetName);
                HttpURLConnection conn = (HttpURLConnection) new URL(url).openConnection();
                conn.setConnectTimeout(15000);
                conn.setReadTimeout(60000);
                conn.setInstanceFollowRedirects(true);
                conn.setRequestProperty("Accept", "application/octet-stream");
                try (InputStream in = conn.getInputStream(); FileOutputStream fos = new FileOutputStream(out)) {
                    byte[] buf = new byte[64 * 1024];
                    int n;
                    while ((n = in.read(buf)) > 0) {
                        fos.write(buf, 0, n);
                    }
                } finally {
                    conn.disconnect();
                }
                installApkFile(appContext, out);
            } catch (Exception ex) {
                showToast(appContext, "更新包下载失败：" + ex.getMessage());
            }
        }).start();
        return "";
    }

    private static void showToast(Context context, String message) {
        android.os.Handler handler = new android.os.Handler(context.getMainLooper());
        handler.post(() -> android.widget.Toast.makeText(context, message, android.widget.Toast.LENGTH_LONG).show());
    }

    private static void installApkFile(Context context, File file) {
        Uri uri = FileProvider.getUriForFile(context, context.getPackageName() + ".fileprovider", file);
        Intent intent = new Intent(Intent.ACTION_VIEW);
        intent.setDataAndType(uri, "application/vnd.android.package-archive");
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
        context.startActivity(intent);
    }

    @PluginMethod
    public void download(PluginCall call) {
        String url = call.getString("url");
        String filename = sanitizeFilename(call.getString("filename"));

        if (TextUtils.isEmpty(url)) {
            call.reject("url is required");
            return;
        }

        if (TextUtils.isEmpty(filename)) {
            filename = sanitizeFilename(URLUtil.guessFileName(url, null, null));
        }
        if (TextUtils.isEmpty(filename)) {
            filename = "download";
        }

        DownloadManager downloadManager =
            (DownloadManager) getContext().getSystemService(Context.DOWNLOAD_SERVICE);
        if (downloadManager == null) {
            call.reject("DownloadManager is unavailable");
            return;
        }

        try {
            long downloadId = enqueueDownload(downloadManager, url, filename);

            JSObject result = new JSObject();
            result.put("downloadId", downloadId);
            result.put("filename", filename);
            result.put("directory", Environment.DIRECTORY_DOWNLOADS);
            call.resolve(result);
        } catch (Exception ex) {
            call.reject("Failed to enqueue download: " + ex.getMessage(), ex);
        }
    }

    @PluginMethod
    public void saveBase64(PluginCall call) {
        String dataBase64 = call.getString("dataBase64");
        String filename = sanitizeFilename(call.getString("filename"));
        String mimeType = call.getString("mimeType");

        if (TextUtils.isEmpty(dataBase64)) {
            call.reject("download content is empty");
            return;
        }
        if (TextUtils.isEmpty(filename)) {
            filename = "download";
        }
        if (TextUtils.isEmpty(mimeType)) {
            mimeType = "application/octet-stream";
        }

        try {
            byte[] data = Base64.decode(dataBase64, Base64.DEFAULT);
            String path = saveBytesToDownloads(getContext(), data, filename, mimeType);

            JSObject result = new JSObject();
            result.put("filename", filename);
            result.put("directory", Environment.DIRECTORY_DOWNLOADS);
            result.put("path", path);
            call.resolve(result);
        } catch (Exception ex) {
            call.reject("Failed to save download: " + ex.getMessage(), ex);
        }
    }

    static long enqueueDownload(DownloadManager downloadManager, String url, String filename) {
        DownloadManager.Request request = new DownloadManager.Request(Uri.parse(url));
        request.setTitle(filename);
        request.setDescription("Downloading file");
        request.setNotificationVisibility(
            DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED
        );
        request.setAllowedOverMetered(true);
        request.setAllowedOverRoaming(true);
        request.setVisibleInDownloadsUi(true);
        if (filename.toLowerCase().endsWith(".apk")) {
            request.setMimeType("application/vnd.android.package-archive");
            request.setDescription("Downloading app update");
        }
        request.setDestinationInExternalPublicDir(
            Environment.DIRECTORY_DOWNLOADS,
            filename
        );
        String cookies = CookieManager.getInstance().getCookie(url);
        if (!TextUtils.isEmpty(cookies)) {
            request.addRequestHeader("Cookie", cookies);
        }

        return downloadManager.enqueue(request);
    }

    static String saveBytesToDownloads(Context context, byte[] data, String filename, String mimeType) throws Exception {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ContentValues values = new ContentValues();
            values.put(MediaStore.Downloads.DISPLAY_NAME, filename);
            values.put(MediaStore.Downloads.MIME_TYPE, mimeType);
            values.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS);
            values.put(MediaStore.Downloads.IS_PENDING, 1);

            Uri uri = context.getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values);
            if (uri == null) {
                throw new IllegalStateException("MediaStore insert returned null");
            }

            try (OutputStream output = context.getContentResolver().openOutputStream(uri)) {
                if (output == null) {
                    throw new IllegalStateException("MediaStore output stream unavailable");
                }
                output.write(data);
                output.flush();
            } catch (Exception ex) {
                context.getContentResolver().delete(uri, null, null);
                throw ex;
            }

            ContentValues done = new ContentValues();
            done.put(MediaStore.Downloads.IS_PENDING, 0);
            context.getContentResolver().update(uri, done, null, null);
            return uri.toString();
        }

        File downloadsDir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
        if (!downloadsDir.exists() && !downloadsDir.mkdirs()) {
            throw new IllegalStateException("Downloads directory unavailable");
        }
        File outputFile = uniqueFile(downloadsDir, filename);
        try (OutputStream output = new FileOutputStream(outputFile)) {
            output.write(data);
            output.flush();
        }
        return outputFile.getAbsolutePath();
    }

    private static File uniqueFile(File directory, String filename) {
        File candidate = new File(directory, filename);
        if (!candidate.exists()) {
            return candidate;
        }
        String base = filename;
        String ext = "";
        int dot = filename.lastIndexOf('.');
        if (dot > 0) {
            base = filename.substring(0, dot);
            ext = filename.substring(dot);
        }
        int index = 1;
        do {
            candidate = new File(directory, base + " (" + index + ")" + ext);
            index += 1;
        } while (candidate.exists());
        return candidate;
    }

    static String sanitizeFilename(String filename) {
        if (filename == null) {
            return "";
        }
        return filename
            .trim()
            .replace("\\", "_")
            .replace("/", "_")
            .replace(":", "_")
            .replace("*", "_")
            .replace("?", "_")
            .replace("\"", "_")
            .replace("<", "_")
            .replace(">", "_")
            .replace("|", "_");
    }
}
