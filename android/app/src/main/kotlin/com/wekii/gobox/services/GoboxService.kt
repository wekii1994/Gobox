package com.wekii.gobox.services

import android.annotation.SuppressLint
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Binder
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.text.SpannableString
import android.text.Spanned
import android.text.style.RelativeSizeSpan
import androidx.core.app.NotificationCompat
import com.wekii.gobox.GlobalState
import com.wekii.gobox.R
import com.wekii.gobox.core.Core
import com.wekii.gobox.models.VpnOptions
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

class GoboxService : Service(), BaseServiceInterface {

    @Volatile
    private var cachedBuilder: NotificationCompat.Builder? = null
    private val binder = LocalBinder()
    @Volatile
    private var hasStartedForeground = false

    private val fairMemoryHelper = FairMemoryHelper("GoboxService")
    private var screenReceiver: BroadcastReceiver? = null

    @Volatile
    private var lastNotificationText: String? = null

    @Volatile
    private var pendingSpeedProfile: String? = null

    @Volatile
    private var pendingSpeedInfo: String? = null

    override fun onCreate() {
        super.onCreate()
        screenReceiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context?, intent: Intent?) {
                when (intent?.action) {
                    Intent.ACTION_SCREEN_OFF -> {
                        GlobalState.getCurrentVPNPlugin()?.notifyScreenStateChanged(false)
                        return
                    }
                    Intent.ACTION_SCREEN_ON -> {
                        GlobalState.getCurrentVPNPlugin()?.notifyScreenStateChanged(true)
                    }
                }
                lastNotificationText = null
                resetNotificationBuilder()
                CoroutineScope(Dispatchers.Main).launch {
                    startForeground()
                }
            }
        }
        val filter = IntentFilter().apply {
            addAction(Intent.ACTION_USER_PRESENT)
            addAction(Intent.ACTION_SCREEN_ON)
            addAction(Intent.ACTION_SCREEN_OFF)
        }
        registerReceiver(screenReceiver, filter, if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) Context.RECEIVER_NOT_EXPORTED else 0)

        fairMemoryHelper.register(
            context = this,
            onTrim = {
                GlobalState.getCurrentVPNPlugin()?.requestGc()
                Core.purge(all = true)
            },
            onKill = { /* system will kill process; service has no extra state to save */ }
        )
    }

    inner class LocalBinder : Binder() {
        fun getService() = this@GoboxService
    }

    override suspend fun start(options: VpnOptions) = 0

    override fun stop() {
        hasStartedForeground = false
        lastNotificationText = null
        pendingSpeedProfile = null
        pendingSpeedInfo = null

        runCatching {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                stopForeground(STOP_FOREGROUND_REMOVE)
            } else {
                stopForeground(true)
            }
        }.onFailure { android.util.Log.e("GoboxService", "Failed to stop foreground: ${it.message}") }

        runCatching {
            getSystemService(android.app.NotificationManager::class.java)
                ?.cancel(GlobalState.NOTIFICATION_ID)
        }.onFailure { android.util.Log.e("GoboxService", "Failed to cancel notification: ${it.message}") }

        stopSelf()
    }

    fun resetNotificationBuilder() {
        cachedBuilder = null
    }

    private suspend fun notificationBuilder() =
        cachedBuilder ?: createGoboxNotificationBuilder().also { cachedBuilder = it }

    @SuppressLint("ForegroundServiceType")
    override suspend fun startForeground() {
        val isSuspended = GlobalState.isSmartStopped
        val isHighPriority = GlobalState.isNotificationHighPriority
        ensureNotificationChannel(isSuspended, isHighPriority)
        val title: String
        val content: String
        if (isSuspended) {
            title = getString(R.string.core_suspended)
            content = getString(R.string.smart_auto_stop_service_running)
        } else {
            title = pendingSpeedProfile?.takeIf { it.isNotBlank() }
                ?: GlobalState.currentProfileName.takeIf { it.isNotBlank() }
                ?: getString(R.string.core_connected)
            content = getString(R.string.service_running)
        }

        lastNotificationText = null
        val pendingProfile = pendingSpeedProfile
        val pendingSpeed = pendingSpeedInfo
        if (hasStartedForeground && !isSuspended && GlobalState.isSpeedNotificationEnabled && pendingProfile != null && pendingSpeed != null) {
            updateNotificationSpeed(pendingProfile, pendingSpeed)
            return
        }

        val builder = createGoboxNotificationBuilder(isSuspended, isHighPriority)
        val notification = builder
            .setContentTitle(title)
            .setContentText(content)
            .setStyle(null)
            .build()

        if (!hasStartedForeground) {
            this.startForeground(notification, useSpecialType = !isSuspended)
            hasStartedForeground = true
        } else {
            getSystemService(android.app.NotificationManager::class.java)?.notify(GlobalState.NOTIFICATION_ID, notification)
        }
    }

    internal suspend fun restoreNotification() {
        GlobalState.isSpeedNotificationEnabled = false
        pendingSpeedInfo = null
        if (hasStartedForeground) {
            startForeground()
        }
    }

    @SuppressLint("ForegroundServiceType")
    internal suspend fun updateNotificationTitle(profileName: String) {
        pendingSpeedProfile = profileName
        val content = if (GlobalState.isSpeedNotificationEnabled) {
            pendingSpeedInfo ?: getString(R.string.service_running)
        } else {
            pendingSpeedInfo = null
            getString(R.string.service_running)
        }
        postNotificationContent(profileName, content)
    }

    @SuppressLint("ForegroundServiceType")
    internal suspend fun updateNotificationSpeed(profileName: String, speedInfo: String) {
        pendingSpeedProfile = profileName
        pendingSpeedInfo = speedInfo
        postNotificationContent(profileName, speedInfo)
    }

    @SuppressLint("ForegroundServiceType")
    private suspend fun postNotificationContent(title: String, content: String) {
        val powerManager = getSystemService(Context.POWER_SERVICE) as? PowerManager
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
            hasStartedForeground = true
            runCatching {
                this.startForeground(notification, useSpecialType = !GlobalState.isSmartStopped)
            }.onFailure { android.util.Log.e("GoboxService", "postNotificationContent startForeground error: ${it.message}") }
        } else {
            runCatching {
                getSystemService(android.app.NotificationManager::class.java)
                    ?.notify(GlobalState.NOTIFICATION_ID, notification)
            }.onFailure { android.util.Log.e("GoboxService", "postNotificationContent notify error: ${it.message}") }
        }
    }

    override fun onTrimMemory(level: Int) {
        super.onTrimMemory(level)
        if (level == 10 || level == 15 || level >= 40) {
            GlobalState.getCurrentVPNPlugin()?.requestGc()
            GlobalState.notifyLowMemory()
            Core.purge(all = false)
        }
    }

    override fun onBind(intent: Intent): IBinder = binder

    override fun onDestroy() {
        stop()
        screenReceiver?.let {
            unregisterReceiver(it)
            screenReceiver = null
        }
        fairMemoryHelper.unregister(this)
        super.onDestroy()
    }
}
