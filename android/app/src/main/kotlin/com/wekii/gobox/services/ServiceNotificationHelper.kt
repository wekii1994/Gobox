package com.wekii.gobox.services

import android.annotation.SuppressLint
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.os.Build
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import com.wekii.gobox.GlobalState
import com.wekii.gobox.R
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Serializes foreground notification state transitions and updates for background proxy services.
 */
internal class ServiceNotificationHelper(
    private val service: Service,
    private val tag: String
) {
    private val mutex = Mutex()

    @Volatile
    private var isStopped = false

    @Volatile
    var hasStartedForeground = false
        private set

    @Volatile
    private var cachedBuilder: NotificationCompat.Builder? = null

    @Volatile
    private var lastNotificationText: String? = null

    @Volatile
    private var pendingSpeedProfile: String? = null

    @Volatile
    private var pendingSpeedInfo: String? = null

    fun resetNotificationBuilder(clearLastText: Boolean = false) {
        cachedBuilder = null
        if (clearLastText) {
            lastNotificationText = null
        }
    }

    private suspend fun notificationBuilder(): NotificationCompat.Builder =
        cachedBuilder ?: service.createGoboxNotificationBuilder().also { cachedBuilder = it }

    suspend fun startForeground() {
        mutex.withLock {
            startForegroundLocked()
        }
    }

    suspend fun restoreNotification() {
        // Flip state synchronously on the caller before awaiting the mutex so later speed
        // updates queued behind this call are not dropped as stale.
        GlobalState.isSpeedNotificationEnabled = false
        mutex.withLock {
            if (isStopped) return@withLock
            pendingSpeedInfo = null
            if (hasStartedForeground) {
                startForegroundLocked()
            }
        }
    }

    suspend fun updateNotificationTitle(profileName: String) {
        GlobalState.isSpeedNotificationEnabled = false
        mutex.withLock {
            if (isStopped) return@withLock
            pendingSpeedProfile = profileName
            pendingSpeedInfo = null
            val content = service.getString(R.string.service_running)
            postNotificationContentLocked(profileName, content)
        }
    }

    suspend fun updateNotificationSpeed(profileName: String, speedInfo: String) {
        mutex.withLock {
            if (isStopped || !GlobalState.isSpeedNotificationEnabled) return@withLock
            pendingSpeedProfile = profileName
            pendingSpeedInfo = speedInfo
            postNotificationContentLocked(profileName, speedInfo)
        }
    }

    fun stop(): Boolean {
        if (isStopped) return false
        isStopped = true
        hasStartedForeground = false
        lastNotificationText = null
        pendingSpeedProfile = null
        pendingSpeedInfo = null
        cachedBuilder = null

        runCatching {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                service.stopForeground(Service.STOP_FOREGROUND_REMOVE)
            } else {
                @Suppress("DEPRECATION")
                service.stopForeground(true)
            }
        }.onFailure { Log.e(tag, "Failed to stop foreground: ${it.message}") }

        runCatching {
            service.getSystemService(NotificationManager::class.java)
                ?.cancel(GlobalState.NOTIFICATION_ID)
        }.onFailure { Log.e(tag, "Failed to cancel notification: ${it.message}") }

        return true
    }

    @SuppressLint("ForegroundServiceType")
    private suspend fun startForegroundLocked() {
        if (isStopped) return

        val isSuspended = GlobalState.isSmartStopped
        val isHighPriority = GlobalState.isNotificationHighPriority
        service.ensureNotificationChannel(isSuspended, isHighPriority)

        val speedProfile = pendingSpeedProfile
        val speedInfo = pendingSpeedInfo
        val showCachedSpeed = !isSuspended &&
            GlobalState.isSpeedNotificationEnabled &&
            !speedProfile.isNullOrBlank() &&
            !speedInfo.isNullOrBlank()

        val title = when {
            isSuspended -> service.getString(R.string.core_suspended)
            showCachedSpeed -> speedProfile!!
            else -> speedProfile?.takeIf { it.isNotBlank() }
                ?: GlobalState.currentProfileName.takeIf { it.isNotBlank() }
                ?: service.getString(R.string.core_connected)
        }
        val content = when {
            isSuspended -> service.getString(R.string.smart_auto_stop_service_running)
            showCachedSpeed -> speedInfo!!
            else -> service.getString(R.string.service_running)
        }

        lastNotificationText = if (!isSuspended) "$title\n$content" else null

        val builder = service.createGoboxNotificationBuilder(isSuspended, isHighPriority)
        val notification = builder
            .setContentTitle(title)
            .setContentText(content)
            .setStyle(null)
            .build()

        // Always invoke service.startForeground even when cached speed is active so Android 14+
        // foreground service types and notification channels stay synchronized.
        val started = service.startForeground(notification, useSpecialType = !isSuspended)
        if (started) {
            hasStartedForeground = true
        } else {
            Log.e(tag, "startForeground failed, falling back to notify")
            val notified = runCatching {
                service.getSystemService(NotificationManager::class.java)
                    ?.notify(GlobalState.NOTIFICATION_ID, notification)
            }.isSuccess
            if (!notified) {
                lastNotificationText = null
            }
        }
    }

    @SuppressLint("ForegroundServiceType")
    private suspend fun postNotificationContentLocked(title: String, content: String) {
        if (isStopped) return

        val powerManager = service.getSystemService(Context.POWER_SERVICE) as? PowerManager
        if (powerManager?.isInteractive == false) {
            return
        }

        if (GlobalState.isSmartStopped) {
            return
        }

        val combinedText = "$title\n$content"
        if (combinedText == lastNotificationText) {
            return
        }
        lastNotificationText = combinedText

        val notification = notificationBuilder()
            .setContentTitle(title)
            .setContentText(content)
            .setStyle(null)
            .build()

        if (!hasStartedForeground) {
            val started = service.startForeground(notification, useSpecialType = true)
            if (started) {
                hasStartedForeground = true
                return
            }
            Log.e(tag, "postNotificationContent startForeground failed, falling back to notify")
        }

        val notified = runCatching {
            service.getSystemService(NotificationManager::class.java)
                ?.notify(GlobalState.NOTIFICATION_ID, notification)
        }.onFailure {
            Log.e(tag, "postNotificationContent notify error: ${it.message}")
        }.isSuccess
        if (!notified) {
            lastNotificationText = null
        }
    }
}
