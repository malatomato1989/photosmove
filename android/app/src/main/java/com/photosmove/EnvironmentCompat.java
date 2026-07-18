package com.photosmove;

import android.os.Environment;

import java.io.File;

class EnvironmentCompat {
    static File getExternalStorage() {
        return Environment.getExternalStorageDirectory();
    }
}
