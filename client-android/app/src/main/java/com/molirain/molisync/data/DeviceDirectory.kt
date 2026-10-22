package com.molirain.molisync.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

class DeviceDirectory {
    private val _devices = MutableStateFlow<Map<String, DeviceTarget>>(emptyMap())
    val devices: StateFlow<Map<String, DeviceTarget>> = _devices.asStateFlow()

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
    }
}
