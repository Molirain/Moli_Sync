package com.molirain.molisync.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

class DeviceDirectory {
    private val _devices = MutableStateFlow<Map<String, DeviceTarget>>(emptyMap())
    val devices: StateFlow<Map<String, DeviceTarget>> = _devices.asStateFlow()

    private val _self = MutableStateFlow<DeviceTarget?>(null)
    val self: StateFlow<DeviceTarget?> = _self.asStateFlow()

    val onlineCount: Int
        get() = _devices.value.size + if (_self.value != null) 1 else 0

    fun setSelf(device: DeviceTarget?) {
        _self.value = device
    }

    fun replaceAll(devices: Collection<DeviceTarget>) {
        _devices.value = devices.associateBy { it.deviceId }
    }

    fun upsert(device: DeviceTarget) {
        _devices.update { current -> current + (device.deviceId to device) }
    }

    fun remove(deviceId: String) {
        _devices.update { current -> current - deviceId }
    }

    fun clear() {
        _devices.value = emptyMap()
        _self.value = null
    }
}
