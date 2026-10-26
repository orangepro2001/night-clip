package com.nightclip.app.platform

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.distinctUntilChanged

/** Whether any VPN network exists (Tailscale runs as a VPN). Advisory only: it never gates a feature. */
object NetworkMonitor {
    fun vpnActive(context: Context): Flow<Boolean> = callbackFlow {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        if (cm == null) {
            trySend(true) // cannot tell: do not nag
            awaitClose { }
            return@callbackFlow
        }
        val networks = mutableSetOf<Network>()
        // Seed with what already exists so the banner does not flash while the callback registers.
        @Suppress("DEPRECATION")
        for (n in cm.allNetworks) {
            val caps = cm.getNetworkCapabilities(n)
            if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) networks += n
        }
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                synchronized(networks) { networks += network; trySend(networks.isNotEmpty()) }
            }

            override fun onLost(network: Network) {
                synchronized(networks) { networks -= network; trySend(networks.isNotEmpty()) }
            }
        }
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_VPN)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) // VPN networks do not have it
            .build()
        trySend(networks.isNotEmpty())
        try {
            cm.registerNetworkCallback(request, cb)
        } catch (e: SecurityException) {
            trySend(true)
        }
        awaitClose { runCatching { cm.unregisterNetworkCallback(cb) } }
    }.distinctUntilChanged()
}
