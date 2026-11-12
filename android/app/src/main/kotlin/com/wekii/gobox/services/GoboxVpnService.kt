package com.wekii.gobox.services

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ProxyInfo
import android.net.VpnService
import android.os.Binder
import android.os.Build
import android.os.IBinder
import android.os.Parcel
import android.util.Log
import com.wekii.gobox.GlobalState
import com.wekii.gobox.core.Core
import com.wekii.gobox.extensions.getIpv4RouteAddress
import com.wekii.gobox.extensions.getIpv6RouteAddress
import com.wekii.gobox.extensions.toCIDR
import com.wekii.gobox.models.AccessControlMode
import com.wekii.gobox.models.VpnOptions
import com.wekii.gobox.plugins.VpnPlugin
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

class GoboxVpnService : VpnService(), BaseServiceInterface {
    companion object {
        private const val TAG = "GoboxVpnService"
        private val proxyExclusionPattern =
            Regex("[a-zA-Z0-9*]+(-[a-zA-Z0-9*]+)*(\\.[a-zA-Z0-9*]+(-[a-zA-Z0-9*]+)*)*")
    }

    private var unlockReceiver: BroadcastReceiver? = null
    private val fairMemoryHelper = FairMemoryHelper(TAG)
    private val notificationHelper = ServiceNotificationHelper(this, TAG)

    override fun onCreate() {
        super.onCreate()
        GlobalState.initServiceEngine()

        unlockReceiver = object : BroadcastReceiver() {
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
        registerReceiver(unlockReceiver, filter, if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) Context.RECEIVER_NOT_EXPORTED else 0)

        fairMemoryHelper.register(
            context = this,
            onTrim = {
                GlobalState.getCurrentVPNPlugin()?.requestGc()
                Core.purge(all = true)
            },
            onKill = { /* system will kill process; service has no extra state to save */ }
        )
    }

    override suspend fun start(options: VpnOptions): Int = with(Builder()) {
        options.ipv4Address.takeIf { it.isNotEmpty() }?.let { ipv4 ->
            val cidr = ipv4.toCIDR()
            addAddress(cidr.address, cidr.prefixLength)
            Log.d("addAddress", "address: ${cidr.address} prefixLength:${cidr.prefixLength}")
            val routes = options.getIpv4RouteAddress()
            if (routes.isNotEmpty()) {
                runCatching {
                    routes.forEach { addRoute(it.address, it.prefixLength) }
                    if (routes.none { it.address.isAnyLocalAddress }) addRoute("0.0.0.0", 32)
                }.onFailure { addRoute("0.0.0.0", 0) }
            } else {
                addRoute("0.0.0.0", 0)
            }
        } ?: addRoute("0.0.0.0", 0)

        if (options.ipv6Address.isNotEmpty()) {
            runCatching {
                val cidr = options.ipv6Address.toCIDR()
                Log.d("addAddress6", "address: ${cidr.address} prefixLength:${cidr.prefixLength}")
                addAddress(cidr.address, cidr.prefixLength)
                val routes = options.getIpv6RouteAddress()
                if (routes.isNotEmpty()) {
                    runCatching { routes.forEach { addRoute(it.address, it.prefixLength) } }
                        .onFailure { addRoute("::", 0) }
                } else {
                    addRoute("::", 0)
                }
            }.onFailure { Log.d("addAddress6", "IPv6 is not supported.") }
        }

        if (options.dnsServerAddress.isNotBlank()) {
            runCatching { addDnsServer(options.dnsServerAddress) }
                .onFailure { Log.e(TAG, "Invalid DNS: ${options.dnsServerAddress}") }
        }

        setMtu(options.mtu.takeIf { it in 1280..65535 } ?: 9000)

        val accessControl = options.accessControl
        if (accessControl.enable) {
            when (accessControl.mode) {
                AccessControlMode.acceptSelected -> {
                    (accessControl.acceptList + packageName).filter { it.isNotBlank() }.distinct().forEach { appPkg ->
                        runCatching { addAllowedApplication(appPkg) }
                            .onFailure { Log.e(TAG, "Failed to allow package $appPkg: ${it.message}", it) }
                    }
                }
                AccessControlMode.rejectSelected -> (accessControl.rejectList - packageName).filter { it.isNotBlank() }.distinct().forEach { appPkg ->
                    runCatching { addDisallowedApplication(appPkg) }
                        .onFailure { Log.e(TAG, "Failed to disallow package $appPkg: ${it.message}", it) }
                }
            }
        }

        setSession("Gobox")
        setBlocking(false)
        if (Build.VERSION.SDK_INT >= 29) setMetered(false)
        if (options.allowBypass) allowBypass()

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q && options.systemProxy) {
            val exclusionList = options.bypassDomain.filter { proxyExclusionPattern.matches(it) }
            setHttpProxy(ProxyInfo.buildDirectProxy("127.0.0.1", options.port, exclusionList))
        }

        val fd = runCatching { establish()?.detachFd() }.getOrElse { e ->
            Log.e(TAG, "Establish VPN exception: ${e.message}")
            null
        }
        if (fd != null && fd > 0) {
            return fd
        }
        Log.e(TAG, "Establish VPN rejected by system")
        -1
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == "UPDATE_NOTIFICATION_SPEED") {
            val profileName = intent.getStringExtra("profileName") ?: ""
            val speedInfo = intent.getStringExtra("speedInfo") ?: ""
            GlobalState.currentProfileName = profileName
            GlobalState.isSpeedNotificationEnabled = true
            if (!GlobalState.isSmartStopped && notificationHelper.hasStartedForeground) {
                CoroutineScope(Dispatchers.Main).launch {
                    updateNotificationSpeed(profileName, speedInfo)
                }
            }
        }
        return START_STICKY
    }

    override suspend fun restoreNotification() {
        notificationHelper.restoreNotification()
    }

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

    private val binder = LocalBinder()

    inner class LocalBinder : Binder() {
        fun getService(): GoboxVpnService = this@GoboxVpnService

        override fun onTransact(code: Int, data: Parcel, reply: Parcel?, flags: Int): Boolean =
            runCatching {
                super.onTransact(code, data, reply, flags).also { success ->
                    if (!success) GlobalState.getCurrentTilePlugin()?.handleStop()
                }
            }.getOrElse { Log.e(TAG, "onTransact failed: ${it.message}"); false }
    }

    override fun onBind(intent: Intent?): IBinder? {
        if (intent?.action == VpnService.SERVICE_INTERFACE) {
            return super.onBind(intent)
        }
        return binder
    }

    override fun onUnbind(intent: Intent?): Boolean {
        super.onUnbind(intent)
        return true
    }

    override fun onRevoke() {
        runCatching {
            VpnPlugin.handleStop()
            getSystemService(android.app.NotificationManager::class.java)
                ?.cancel(GlobalState.NOTIFICATION_ID)
        }.onFailure { Log.e(TAG, "onRevoke error: ${it.message}") }
        super.onRevoke()
    }

    override fun onDestroy() {
        stop()
        unlockReceiver?.let {
            unregisterReceiver(it)
            unlockReceiver = null
        }
        fairMemoryHelper.unregister(this)
        super.onDestroy()
    }
}
