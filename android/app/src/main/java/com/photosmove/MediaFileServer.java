package com.photosmove;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.media.MediaMetadataRetriever;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileInputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URLDecoder;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * HTTP server that serves files from Android/data/ directories.
 * Only accessible from localhost (127.0.0.1) — the Go binary fetches
 * restricted files through this server.
 */
public class MediaFileServer extends Thread {

    private static final String TAG = "photosmove";
    private static final byte[] HEADER_OK = "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n\r\n".getBytes();
    private static final byte[] HEADER_403 = "HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n".getBytes();
    private static final byte[] HEADER_404 = "HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n".getBytes();
    private static final byte[] HEADER_500 = "HTTP/1.1 500 Error\r\nConnection: close\r\n\r\n".getBytes();

    private final int port;
    private final String allowedPrefix;
    private final ExecutorService threadPool = Executors.newFixedThreadPool(4);
    private ServerSocket serverSocket;
    private volatile boolean running = true;

    public MediaFileServer(int port) {
        this.port = port;
        String prefix;
        try {
            prefix = EnvironmentCompat.getExternalStorage().getCanonicalPath();
        } catch (Exception e) {
            prefix = EnvironmentCompat.getExternalStorage().getAbsolutePath();
        }
        this.allowedPrefix = prefix;
        setDaemon(true);
    }

    public void shutdown() {
        running = false;
        threadPool.shutdown();
        try {
            if (serverSocket != null && !serverSocket.isClosed()) {
                serverSocket.close();
            }
        } catch (Exception ignored) {}
    }

    @Override
    public void run() {
        try {
            serverSocket = new ServerSocket(port, 50, InetAddress.getByName("127.0.0.1"));
            Log.i(TAG, "MediaFileServer listening on 127.0.0.1:" + port);
            while (running) {
                try {
                    Socket client = serverSocket.accept();
                    threadPool.submit(() -> handleClient(client));
                } catch (Exception e) {
                    if (running) Log.e(TAG, "MediaFileServer accept error", e);
                }
            }
        } catch (Exception e) {
            if (running) Log.e(TAG, "MediaFileServer error", e);
        } finally {
            threadPool.shutdown();
        }
    }

    private void handleClient(Socket client) {
        try {
            client.setSoTimeout(30000);
            BufferedReader reader = new BufferedReader(new InputStreamReader(client.getInputStream()));
            String requestLine = reader.readLine();
            if (requestLine == null) {
                client.close();
                return;
            }

            // Parse: GET /file?path=<encoded_path> HTTP/1.1
            String[] parts = requestLine.split(" ");
            if (parts.length < 2 || !"GET".equals(parts[0])) {
                client.getOutputStream().write(HEADER_403);
                client.close();
                return;
            }
            String uri = parts[1];
            String pathPart = uri.contains("?") ? uri.substring(0, uri.indexOf("?")) : uri;
            String query = uri.contains("?") ? uri.substring(uri.indexOf("?") + 1) : "";

            // Consume remaining headers
            String headerLine;
            while ((headerLine = reader.readLine()) != null && !headerLine.isEmpty()) {
                // consume headers
            }

            String filePath = null;
            for (String param : query.split("&")) {
                String[] kv = param.split("=", 2);
                if (kv.length == 2 && "path".equals(URLDecoder.decode(kv[0], "UTF-8"))) {
                    filePath = URLDecoder.decode(kv[1], "UTF-8");
                }
            }

            if (filePath == null) {
                client.getOutputStream().write(HEADER_404);
                client.close();
                return;
            }

            // Path whitelist: only serve files under external storage
            File file = new File(filePath).getCanonicalFile();
            String canonicalPath = file.getAbsolutePath();
            if (!canonicalPath.equals(allowedPrefix) && !canonicalPath.startsWith(allowedPrefix + "/")) {
                Log.w(TAG, "MediaFileServer: rejected path outside storage: " + canonicalPath);
                client.getOutputStream().write(HEADER_403);
                client.close();
                return;
            }

            if (!file.exists() || !file.isFile()) {
                client.getOutputStream().write(HEADER_404);
                client.close();
                return;
            }

            OutputStream os = client.getOutputStream();

            if ("/convert".equals(pathPart)) {
                // HEIC/HEIF → JPEG conversion using Android BitmapFactory
                String lower = canonicalPath.toLowerCase();
                if (!lower.endsWith(".heic") && !lower.endsWith(".heif")) {
                    os.write("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n".getBytes());
                    return;
                }
                BitmapFactory.Options opts = new BitmapFactory.Options();
                opts.inJustDecodeBounds = true;
                BitmapFactory.decodeFile(canonicalPath, opts);
                if (opts.outWidth > 8192 || opts.outHeight > 8192) {
                    opts.inSampleSize = 2;
                }
                opts.inJustDecodeBounds = false;
                opts.inPreferredConfig = Bitmap.Config.RGB_565;
                Bitmap bitmap = BitmapFactory.decodeFile(canonicalPath, opts);
                if (bitmap == null) {
                    Log.w(TAG, "Failed to decode HEIC: " + canonicalPath);
                    os.write(HEADER_500);
                    return;
                }
                os.write("HTTP/1.1 200 OK\r\nContent-Type: image/jpeg\r\nConnection: close\r\n\r\n".getBytes());
                try {
                    bitmap.compress(Bitmap.CompressFormat.JPEG, 90, os);
                    os.flush();
                } finally {
                    bitmap.recycle();
                }
                return;
            }

            if ("/videothumb".equals(pathPart)) {
                // Extract first frame of a video as JPEG thumbnail
                MediaMetadataRetriever retriever = new MediaMetadataRetriever();
                try {
                    retriever.setDataSource(canonicalPath);
                    Bitmap frame = retriever.getFrameAtTime(0);
                    if (frame == null) {
                        os.write(HEADER_404);
                        os.flush();
                        return;
                    }
                    // Scale down to max 240px wide for thumbnail
                    int w = frame.getWidth();
                    int h = frame.getHeight();
                    if (w > 240) {
                        int newH = Math.round(h * 240f / w);
                        Bitmap scaled = Bitmap.createScaledBitmap(frame, 240, newH, true);
                        frame.recycle();
                        frame = scaled;
                    }
                    os.write("HTTP/1.1 200 OK\r\nContent-Type: image/jpeg\r\nConnection: close\r\n\r\n".getBytes());
                    frame.compress(Bitmap.CompressFormat.JPEG, 80, os);
                    os.flush();
                    frame.recycle();
                } catch (Exception e) {
                    Log.w(TAG, "videothumb failed: " + canonicalPath, e);
                    os.write(HEADER_404);
                    os.flush();
                } finally {
                    try { retriever.release(); } catch (Exception ignored) {}
                }
                return;
            }

            // /file — stream file content
            os.write(("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: " + file.length() + "\r\nConnection: close\r\n\r\n").getBytes());

            try (FileInputStream fis = new FileInputStream(file)) {
                byte[] buf = new byte[65536];
                int len;
                while ((len = fis.read(buf)) > 0) {
                    os.write(buf, 0, len);
                }
            }
        } catch (Exception e) {
            try {
                client.getOutputStream().write(HEADER_500);
            } catch (Exception ignored) {}
        } finally {
            try { client.close(); } catch (Exception ignored) {}
        }
    }
}
