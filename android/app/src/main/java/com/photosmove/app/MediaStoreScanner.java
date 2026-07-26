package com.photosmove.app;

import android.content.ContentResolver;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.provider.MediaStore;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

/**
 * Scans media files using MediaStore ContentProvider.
 * Groups files by BUCKET_ID into albums, classifies them by category
 * (camera, screenshots, app), and exports JSON for Go consumption.
 */
public class MediaStoreScanner {

    private static final String TAG = "photosmove";

    private static final String[] IMAGE_EXTS = {
            ".jpg", ".jpeg", ".png", ".heic", ".heif", ".gif", ".bmp", ".webp",
            ".tiff", ".tif", ".raw", ".dng", ".svg",
    };

    static class FileInfo {
        String relPath;
        String fullPath;
        long size;
        long modTime;
    }

    static class AlbumInfo {
        String path;
        String name;
        String category;
        ArrayList<FileInfo> files = new ArrayList<>();
    }

    public static void scan(Context context, File outputFile) throws Exception {
        ContentResolver resolver = context.getContentResolver();

        // bucket_id -> AlbumInfo
        HashMap<Long, AlbumInfo> albumMap = new HashMap<>();

        // Query images
        queryMedia(resolver, MediaStore.Images.Media.EXTERNAL_CONTENT_URI, albumMap);
        // Query videos
        queryMedia(resolver, MediaStore.Video.Media.EXTERNAL_CONTENT_URI, albumMap);

        Log.i(TAG, "MediaStore scan: found " + albumMap.size() + " albums");

        // Build JSON
        JSONObject root = new JSONObject();
        JSONArray albumsArr = new JSONArray();

        for (Map.Entry<Long, AlbumInfo> entry : albumMap.entrySet()) {
            AlbumInfo album = entry.getValue();
            if (album.files.isEmpty()) continue;

            JSONObject jAlbum = new JSONObject();
            jAlbum.put("path", album.path);
            jAlbum.put("name", album.name);
            jAlbum.put("category", album.category);
            jAlbum.put("file_count", album.files.size());

            long totalSize = 0;
            JSONArray filesArr = new JSONArray();
            for (FileInfo f : album.files) {
                totalSize += f.size;
                JSONObject jf = new JSONObject();
                jf.put("rel_path", f.relPath);
                jf.put("full_path", f.fullPath);
                jf.put("size", f.size);
                jf.put("mod_time", f.modTime);
                filesArr.put(jf);
            }
            jAlbum.put("total_size", totalSize);
            jAlbum.put("files", filesArr);
            albumsArr.put(jAlbum);
        }

        root.put("albums", albumsArr);

        outputFile.getParentFile().mkdirs();
        try (FileOutputStream fos = new FileOutputStream(outputFile)) {
            fos.write(root.toString().getBytes("UTF-8"));
        }

        Log.i(TAG, "MediaStore JSON: " + outputFile.getAbsolutePath()
                + " (" + albumMap.size() + " albums)");
    }

    private static void queryMedia(ContentResolver resolver, Uri uri,
                                   HashMap<Long, AlbumInfo> albumMap) {
        String[] projection = {
                MediaStore.MediaColumns.BUCKET_ID,
                MediaStore.MediaColumns.BUCKET_DISPLAY_NAME,
                MediaStore.MediaColumns.DATA,
                MediaStore.MediaColumns.SIZE,
                MediaStore.MediaColumns.DATE_MODIFIED,
        };

        try (Cursor cursor = resolver.query(uri, projection, null, null, null)) {
            if (cursor == null) return;

            int colBucketId = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.BUCKET_ID);
            int colBucketName = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.BUCKET_DISPLAY_NAME);
            int colData = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.DATA);
            int colSize = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.SIZE);
            int colModTime = cursor.getColumnIndexOrThrow(MediaStore.MediaColumns.DATE_MODIFIED);

            while (cursor.moveToNext()) {
                long size = cursor.getLong(colSize);
                if (size <= 0) continue;

                String dataPath = cursor.getString(colData);
                if (dataPath == null || dataPath.isEmpty()) continue;
                if (dataPath.contains("/Android/data/") || dataPath.contains("/Android/obb/")) continue;

                long bucketId = cursor.getLong(colBucketId);
                String bucketName = cursor.getString(colBucketName);

                // Get album directory
                File file = new File(dataPath);
                File albumDir = file.getParentFile();
                if ((bucketName == null || bucketName.isEmpty()) && albumDir != null) {
                    bucketName = albumDir.getName();
                }
                if (albumDir == null) continue;
                String albumPath = albumDir.getAbsolutePath();

                AlbumInfo album = albumMap.get(bucketId);
                if (album == null) {
                    album = new AlbumInfo();
                    album.path = albumPath;
                    album.name = bucketName;
                    album.category = classifyAlbum(bucketName, albumPath);
                    albumMap.put(bucketId, album);
                }

                FileInfo fi = new FileInfo();
                fi.fullPath = dataPath;
                fi.relPath = getRelativePath(file, albumDir);
                fi.size = size;
                fi.modTime = cursor.getLong(colModTime);
                album.files.add(fi);
            }
        }
    }

    private static String classifyAlbum(String bucketName, String path) {
        String lower = bucketName.toLowerCase(Locale.US);
        if ("camera".equals(lower) || "相机".equals(lower)) {
            return "camera";
        }
        if ("screenshots".equals(lower) || "截屏记录".equals(lower)
                || "screenshot".equals(lower)) {
            return "screenshots";
        }
        return "app";
    }

    private static String getRelativePath(File file, File baseDir) {
        String basePath = baseDir.getAbsolutePath();
        String filePath = file.getAbsolutePath();
        if (filePath.startsWith(basePath + "/")) {
            return filePath.substring(basePath.length() + 1);
        }
        return file.getName();
    }

}
