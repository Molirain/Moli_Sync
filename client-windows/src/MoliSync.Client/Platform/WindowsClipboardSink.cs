using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Platform;

/// <summary>
/// Writes text to the Windows clipboard through Win32 (OpenClipboard /
/// SetClipboardData) on a dedicated STA thread. The API requires a moveable
/// global memory block; ownership of that block is transferred to the system
/// only when SetClipboardData succeeds.
/// </summary>
[SupportedOSPlatform("windows")]
public sealed class WindowsClipboardSink : IClipboardSink, IAsyncDisposable
{
    private const uint CfUnicodeText = 13;
    private const uint GmemMoveable = 0x0002;
    private const int MaximumTextLength = 1_000_000;
    private const int OpenRetries = 20;
    private const int RetryDelayMilliseconds = 50;

    private readonly IClientLog _log;
    private readonly object _gate = new();
    private readonly AutoResetEvent _signal = new(false);
    private readonly Thread _thread;
    private string? _pending;
    private volatile bool _disposed;

    public WindowsClipboardSink(IClientLog log)
    {
        _log = log ?? throw new ArgumentNullException(nameof(log));

        _thread = new Thread(Run)
        {
            IsBackground = true,
            Name = "molisync-clipboard",
        };
        _thread.SetApartmentState(ApartmentState.STA);
        _thread.Start();
    }

    public bool TrySetText(string text)
    {
        if (_disposed || string.IsNullOrEmpty(text) || text.Length > MaximumTextLength)
        {
            return false;
        }

        lock (_gate)
        {
            // Latest-wins: a burst of clipboard updates must not build a queue
            // of stale text.
            _pending = text;
        }
        _signal.Set();
        return true;
    }

    public ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return ValueTask.CompletedTask;
        }

        _disposed = true;
        _signal.Set();
        if (_thread.IsAlive)
        {
            _thread.Join(TimeSpan.FromSeconds(2));
        }
        _signal.Dispose();
        return ValueTask.CompletedTask;
    }

    private void Run()
    {
        while (true)
        {
            _signal.WaitOne();
            if (_disposed)
            {
                return;
            }

            string? text;
            lock (_gate)
            {
                text = _pending;
                _pending = null;
            }

            if (text is not null && !SetTextCore(text))
            {
                _log.Warn("clipboard is held by another process; text was not copied");
            }
        }
    }

    private static bool SetTextCore(string text)
    {
        for (var attempt = 0; attempt < OpenRetries; attempt++)
        {
            if (!OpenClipboard(IntPtr.Zero))
            {
                Thread.Sleep(RetryDelayMilliseconds);
                continue;
            }

            var ownedHandle = IntPtr.Zero;
            try
            {
                if (!EmptyClipboard())
                {
                    Thread.Sleep(RetryDelayMilliseconds);
                    continue;
                }

                var byteCount = checked((text.Length + 1) * sizeof(char));
                ownedHandle = GlobalAlloc(GmemMoveable, (UIntPtr)(uint)byteCount);
                if (ownedHandle == IntPtr.Zero)
                {
                    return false;
                }

                var destination = GlobalLock(ownedHandle);
                if (destination == IntPtr.Zero)
                {
                    return false;
                }

                try
                {
                    Marshal.Copy(text.ToCharArray(), 0, destination, text.Length);
                    Marshal.WriteInt16(destination, text.Length * sizeof(char), 0);
                }
                finally
                {
                    GlobalUnlock(ownedHandle);
                }

                if (SetClipboardData(CfUnicodeText, ownedHandle) == IntPtr.Zero)
                {
                    return false;
                }

                // The clipboard now owns the block and will free it.
                ownedHandle = IntPtr.Zero;
                return true;
            }
            finally
            {
                if (ownedHandle != IntPtr.Zero)
                {
                    GlobalFree(ownedHandle);
                }
                CloseClipboard();
            }
        }

        return false;
    }

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool OpenClipboard(IntPtr hWndNewOwner);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool EmptyClipboard();

    [DllImport("user32.dll", SetLastError = true)]
    private static extern IntPtr SetClipboardData(uint uFormat, IntPtr hMem);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CloseClipboard();

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GlobalAlloc(uint uFlags, UIntPtr dwBytes);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GlobalLock(IntPtr hMem);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GlobalUnlock(IntPtr hMem);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GlobalFree(IntPtr hMem);
}
