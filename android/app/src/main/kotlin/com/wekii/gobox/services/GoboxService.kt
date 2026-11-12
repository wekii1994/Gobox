package com.wekii.gobox.services

import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Binder
import android.os.Build
import android.os.IBinder
import com.wekii.gobox.GlobalState
import com.wekii.gobox.core.Core
import com.wekii.gobox.models.VpnOptions
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

class GoboxService : Service(), BaseServiceInterface {

    private val binder = LocalBinder()
    private val fairMemoryHelper = FairMemoryHelper("GoboxService")
    private val notificationHelper = ServiceNotificationHelper(this, "GoboxService")
    private var screenReceiver: BroadcastReceiver? = null

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
                notificationHelper.resetNotificationBuilder(clearLastText = true)
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
        if (!notificationHelper.stop()) return
        stopSelf()
    }

    override fun resetNotificationBuilder() {
        notificationHelper.resetNotificationBuilder()
    }

    override suspend fun startForeground() {
        notificationHelper.startForeground()
    }

    override suspend fun restoreNotification() {
        notificationHelper.restoreNotification()
    }

    override suspend fun updateNotificationTitle(profileName: String) {
        notificationHelper.updateNotificationTitle(profileName)
    }

    override suspend fun updateNotificationSpeed(profileName: String, speedInfo: String) {
        notificationHelper.updateNotificationSpeed(profileName, speedInfo)
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
