package com.molirain.molisync.data.cloud

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CloudRecordTest {

    private val json = kotlinx.serialization.json.Json { ignoreUnknownKeys = true }

    @Test
    fun `decodes a flat file record from the HTTP API`() {
        val record = json.decodeFromString(
            CloudRecord.serializer(),
            """
            {
              "record_id": "abc123",
              "sender_device_id": "device-a",
              "sender_name": "Pixel 9",
              "created_at": 1700000000000,
              "type": "file",
              "blob_id": "abc123",
              "file_name": "report.pdf",
              "file_size": 1024,
              "mime_type": "application/pdf",
              "blake3_hash": "ff00",
              "future_field": true
            }
            """.trimIndent(),
        )

        assertEquals("abc123", record.recordId)
        assertEquals("Pixel 9", record.senderName)
        assertEquals(CloudKind.FILE, record.kind)
        assertEquals(1024L, record.fileSize)
        assertTrue(record.isFile)
        assertTrue(record.hasBlob)
        assertFalse(record.isText)
        assertEquals("report.pdf", record.displayName)
    }

    @Test
    fun `decodes a text record list and ignores missing optional fields`() {
        val response = json.decodeFromString(
            RecordsResponse.serializer(),
            """
            {"records":[
              {"record_id":"t1","type":"text","text_content":"hello","created_at":1},
              {"record_id":"i1","type":"image","file_name":"photo.jpg","file_size":2048,"blob_id":"i1"}
            ]}
            """.trimIndent(),
        )

        assertEquals(2, response.records.size)
        assertTrue(response.records[0].isText)
        assertEquals("hello", response.records[0].text)
        assertTrue(response.records[1].isImage)
        assertEquals(2048L, response.records[1].fileSize)
    }

    @Test
    fun `decodes the standard API error envelope`() {
        val envelope = json.decodeFromString(
            ApiErrorEnvelope.serializer(),
            """{"error":{"code":"too_large","message":"item exceeds the configured size limit"}}""",
        )

        assertEquals("too_large", envelope.error.code)
        assertEquals("item exceeds the configured size limit", envelope.error.message)
    }
}
