package com.wekii.gobox.services

import android.annotation.SuppressLint
import android.app.Notification
import android.app.Notification.FOREGROUND_SERVICE_IMMEDIATE
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.core.graphics.drawable.toBitmap
import com.wekii.gobox.GlobalState
import com.wekii.gobox.R
import com.wekii.gobox.models.VpnOptions
import com.wekii.gobox.plugins.ServicePlugin
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.withContext
import android.content.ComponentName
import android.content.Intent


interface BaseServiceInterface {
    suspend fun start(options: VpnOptions): Int
    fun stop()
    suspend fun startForeground()
    fun resetNotificationBuilder()
    suspend fun restoreNotification()
    suspend fun updateNotificationTitle(profileName: String)
    suspend fun updateNotificationSpeed(profileName: String, speedInfo: String)
}

suspend fun Service.createGoboxNotificationBuilder(
    isSuspended: Boolean = GlobalState.isSmartStopped,
    isHighPriority: Boolean = GlobalState.isNotificationHighPriority
): NotificationCompat.Builder =
    withContext(Dispatchers.IO) {
        val intent = (packageManager.getLaunchIntentForPackage(packageName) ?: Intent().apply {
            component = ComponentName(packageName, "com.wekii.gobox.MainActivity")
            action = Intent.ACTION_MAIN
            addCategory(Intent.CATEGORY_LAUNCHER)
        }).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }

        val flags = if (Build.VERSION.SDK_INT >= 31) {
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        } else {
            PendingIntent.FLAG_UPDATE_CURRENT
        }
        val pendingIntent = withContext(Dispatchers.Main) {
            PendingIntent.getActivity(this@createGoboxNotificationBuilder, 0, intent, flags)
        }

        val largeIconBitmap = if (isSuspended) {
            runCatching {
                ContextCompat.getDrawable(this@createGoboxNotificationBuilder, R.mipmap.ic_launcher)?.toBitmap()
            }.getOrNull()
        } else {
            null
        }

        val channelId = when {
            isSuspended -> GlobalState.NOTIFICATION_CHANNEL_SUSPENDED
            isHighPriority -> GlobalState.NOTIFICATION_CHANNEL_HIGH
            else -> GlobalState.NOTIFICATION_CHANNEL
        }
        val priority = if (isSuspended || isHighPriority) NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_LOW

        NotificationCompat.Builder(this@createGoboxNotificationBuilder, channelId).apply {
            setSmallIcon(R.drawable.ic)
            if (largeIconBitmap != null) {
                setLargeIcon(largeIconBitmap)
            }
            setContentTitle("Gobox")
            setContentIntent(pendingIntent)
            setCategory(NotificationCompat.CATEGORY_SERVICE)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                foregroundServiceBehavior = FOREGROUND_SERVICE_IMMEDIATE
            }
            setOngoing(true)
            setShowWhen(true)
            setOnlyAlertOnce(true)
            setPriority(priority)
        }
    }

fun Service.ensureNotificationChannel(
    isSuspended: Boolean = GlobalState.isSmartStopped,
    isHighPriority: Boolean = GlobalState.isNotificationHighPriority
) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    val manager = getSystemService(NotificationManager::class.java) ?: return
    val channelId = when {
        isSuspended -> GlobalState.NOTIFICATION_CHANNEL_SUSPENDED
        isHighPriority -> GlobalState.NOTIFICATION_CHANNEL_HIGH
        else -> GlobalState.NOTIFICATION_CHANNEL
    }
    val channel = manager.getNotificationChannel(channelId)
    if (channel == null) {
        val importance = when {
            isSuspended -> NotificationManager.IMPORTANCE_DEFAULT
            isHighPriority -> NotificationManager.IMPORTANCE_HIGH
            else -> NotificationManager.IMPORTANCE_LOW
        }
        val name = when {
            isSuspended -> "Gobox Suspended Service"
            isHighPriority -> "Gobox High Priority Service"
            else -> "Gobox Service"
        }
        val newChannel = NotificationChannel(channelId, name, importance).apply {
            setShowBadge(false)
            if (isSuspended || isHighPriority) {
                setSound(null, null)
                enableVibration(false)
            }
        }
        manager.createNotificationChannel(newChannel)
    }
}

@SuppressLint("ForegroundServiceType")
fun Service.startForeground(notification: Notification, useSpecialType: Boolean = true): Boolean {
    ensureNotificationChannel(GlobalState.isSmartStopped, GlobalState.isNotificationHighPriority)

    val type = if (Build.VERSION.SDK_INT >= 34 && useSpecialType && !GlobalState.isSmartStopped) {
        android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
    } else {
        0
    }

    val primaryResult = runCatching {
        if (Build.VERSION.SDK_INT >= 34 && type != 0) {
            startForeground(GlobalState.NOTIFICATION_ID, notification, type)
        } else {
            startForeground(GlobalState.NOTIFICATION_ID, notification)
        }
    }
    if (primaryResult.isSuccess) return true

    android.util.Log.e("BaseServiceInterface", "startForeground failed: ${primaryResult.exceptionOrNull()?.message}")
    val fallbackResult = runCatching {
        startForeground(GlobalState.NOTIFICATION_ID, notification)
    }
    if (fallbackResult.isSuccess) return true

    val fallbackMsg = fallbackResult.exceptionOrNull()?.message
    android.util.Log.e("BaseServiceInterface", "startForeground fallback failed: $fallbackMsg")
    ServicePlugin.appError("BaseServiceInterface", "Start foreground failed: $fallbackMsg")
    return false
}
