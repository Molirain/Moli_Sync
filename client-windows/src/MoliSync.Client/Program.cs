using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using MoliSync.Client.Http;
using MoliSync.Client.Logging;
using MoliSync.Client.Platform;
using MoliSync.Client.Security;
using MoliSync.Client.Sync;

namespace MoliSync.Client;

public static class Program
{
    public static async Task<int> Main(string[] args)
    {
        try
        {
            return await RunAsync(args).ConfigureAwait(false);
        }
        catch (Exception exception)
        {
            Console.Error.WriteLine($"fatal: {exception.GetType().Name}: {exception.Message}");
            return 1;
        }
    }

    private static async Task<int> RunAsync(string[] args)
    {
        var configPath = Path.Combine(AppContext.BaseDirectory, "client.json");
        var listCertificates = false;
        var headless = false;

        for (var index = 0; index < args.Length; index++)
        {
            switch (args[index])
            {
                case "--config":
                    if (index + 1 >= args.Length)
                    {
                        PrintUsage();
                        return 2;
                    }
                    configPath = args[++index];
                    break;
                case "--list-certificates":
                    listCertificates = true;
                    break;
                case "--headless":
                    headless = true;
                    break;
                case "--help":
                case "-h":
                    PrintUsage();
                    return 0;
                default:
                    Console.Error.WriteLine($"unknown argument: {args[index]}");
                    PrintUsage();
                    return 2;
            }
        }

        var log = new ConsoleClientLog();

        ClientOptions options;
        try
        {
            options = ClientOptions.Load(configPath);
        }
        catch (ConfigurationException exception)
        {
            log.Error(exception.Message);
            return 2;
        }

        PinnedCertificateAuthority authority;
        try
        {
            authority = PinnedCertificateAuthority.Load(options.CaCertificatePath, options.ExpectedIssuerName);
        }
        catch (Exception exception) when (
            exception is ConfigurationException or CryptographicException or IOException)
        {
            log.Error($"cannot load the pinned CA: {exception.Message}");
            return 2;
        }

        using (authority)
        {
            var certificateStore = new WindowsCertificateStore(log);
            if (listCertificates)
            {
                return ListCertificates(certificateStore, authority, options);
            }

            ClientIdentity identity;
            try
            {
                identity = certificateStore.SelectIdentity(
                    authority,
                    new CertificateSelectionCriteria
                    {
                        Thumbprint = options.ClientCertificateThumbprint,
                    });
            }
            catch (ClientCertificateNotFoundException exception)
            {
                log.Error(exception.Message);
                return 2;
            }

            using (identity)
            {
                return await RunClientAsync(options, identity, authority, log, headless).ConfigureAwait(false);
            }
        }
    }

    private static async Task<int> RunClientAsync(
        ClientOptions options,
        ClientIdentity identity,
        PinnedCertificateAuthority authority,
        IClientLog log,
        bool headless)
    {
        try
        {
            Directory.CreateDirectory(options.DownloadDirectory);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            log.Error($"cannot create download directory: {exception.Message}");
            return 2;
        }

        var serverValidator = new PinnedServerCertificateValidator(authority, log);
        using var http = MtlsHttpClientFactory.Create(
            identity,
            serverValidator,
            TimeSpan.FromSeconds(options.ConnectTimeoutSeconds));
        await using var clipboard = new WindowsClipboardSink(log);
        var store = new SyncItemStore(log);
        var dispatcher = new SyncDispatcher(store, clipboard, log, options.AutoCopyText);
        var downloader = new BlobDownloader(http, options.DownloadDirectory, log);
        var coordinator = new SyncCoordinator(store, downloader, log);
        var control = new ControlPlaneClient(options, identity, serverValidator, dispatcher, log);

        using var cancellation = new CancellationTokenSource();
        ConsoleCancelEventHandler cancelHandler = (_, eventArgs) =>
        {
            eventArgs.Cancel = true;
            cancellation.Cancel();
        };
        Console.CancelKeyPress += cancelHandler;

        var printGate = new object();
        store.ItemAdded += item =>
        {
            lock (printGate)
            {
                PrintItem(item);
            }
        };
        store.ItemUpdated += item =>
        {
            if (item.State is SyncItemState.Downloaded or SyncItemState.Failed)
            {
                lock (printGate)
                {
                    PrintUpdate(item);
                }
            }
        };
        dispatcher.PresenceChanged += update =>
        {
            lock (printGate)
            {
                Console.WriteLine($"[PEER] {update.State}: {Display(update.Device.Name)} ({ShortId(update.Device.DeviceId)})");
            }
        };

        log.Info($"server: {options.ServerUrl}");
        log.Info($"device id: {identity.DeviceId}");
        log.Info($"pinned CA: {authority.SubjectName}");
        log.Info($"downloads: {options.DownloadDirectory}");

        var controlTask = control.RunAsync(cancellation.Token);

        if (headless)
        {
            try
            {
                await Task.Delay(Timeout.InfiniteTimeSpan, cancellation.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                // Ctrl+C or process shutdown.
            }
        }
        else
        {
            PrintHelp();
            var commandTask = CommandLoopAsync(store, coordinator, cancellation, printGate, log);
            try
            {
                var completed = await Task.WhenAny(
                        commandTask,
                        Task.Delay(Timeout.InfiniteTimeSpan, cancellation.Token))
                    .ConfigureAwait(false);
                if (completed == commandTask)
                {
                    await commandTask.ConfigureAwait(false);
                }
            }
            catch (OperationCanceledException)
            {
                // Shutdown requested.
            }
            cancellation.Cancel();
        }

        cancellation.Cancel();
        try
        {
            await controlTask.ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            // Normal shutdown.
        }
        finally
        {
            Console.CancelKeyPress -= cancelHandler;
        }

        log.Info("client stopped");
        return 0;
    }

    private static async Task CommandLoopAsync(
        SyncItemStore store,
        SyncCoordinator coordinator,
        CancellationTokenSource cancellation,
        object printGate,
        IClientLog log)
    {
        while (!cancellation.IsCancellationRequested)
        {
            var line = await Console.In.ReadLineAsync().ConfigureAwait(false);
            if (line is null)
            {
                cancellation.Cancel();
                return;
            }

            line = line.Trim();
            if (line.Length == 0)
            {
                continue;
            }

            if (line is "quit" or "exit")
            {
                cancellation.Cancel();
                return;
            }

            if (line is "help" or "?")
            {
                PrintHelp();
                continue;
            }

            if (line == "list")
            {
                lock (printGate)
                {
                    PrintList(store);
                }
                continue;
            }

            if (line.StartsWith("download ", StringComparison.OrdinalIgnoreCase))
            {
                var recordId = line["download ".Length..].Trim();
                StartDownload(store, coordinator, recordId, cancellation.Token, printGate, log);
                continue;
            }

            lock (printGate)
            {
                Console.WriteLine("unknown command; type help");
            }
        }
    }

    private static void StartDownload(
        SyncItemStore store,
        SyncCoordinator coordinator,
        string recordId,
        CancellationToken cancellationToken,
        object printGate,
        IClientLog log)
    {
        var item = store.Get(recordId);
        if (item is null)
        {
            lock (printGate)
            {
                Console.WriteLine($"unknown record: {recordId}");
            }
            return;
        }
        if (item.File is null)
        {
            lock (printGate)
            {
                Console.WriteLine($"record {recordId} has no downloadable file");
            }
            return;
        }

        var total = item.File.FileSize;
        var lastPercent = -10;
        var progress = new Progress<long>(value =>
        {
            var percent = total == 0 ? 100 : (int)Math.Min(100, value * 100 / total);
            lock (printGate)
            {
                if (percent < lastPercent + 10 && value < total)
                {
                    return;
                }
                lastPercent = percent;
                Console.WriteLine($"[DL] {ShortId(recordId)} {percent}% ({FormatBytes(value)}/{FormatBytes(total)})");
            }
        });

        _ = Task.Run(
            async () =>
            {
                try
                {
                    var downloaded = await coordinator
                        .DownloadAsync(recordId, progress, cancellationToken)
                        .ConfigureAwait(false);
                    lock (printGate)
                    {
                        Console.WriteLine(
                            $"[DONE] {Display(downloaded.File?.FileName ?? "?")} -> {Display(downloaded.LocalPath ?? "?")}");
                    }
                }
                catch (OperationCanceledException)
                {
                    // Shutdown or user cancellation.
                }
                catch (Exception exception)
                {
                    log.Warn($"download failed: {exception.Message}");
                }
            },
            CancellationToken.None);
    }

    private static int ListCertificates(
        WindowsCertificateStore certificateStore,
        PinnedCertificateAuthority authority,
        ClientOptions options)
    {
        var certificates = certificateStore.EnumerateCurrentUserPersonal();
        try
        {
            Console.WriteLine($"CurrentUser\\MY contains {certificates.Count} certificate(s):");
            foreach (var certificate in certificates.OrderBy(c => c.Subject, StringComparer.OrdinalIgnoreCase))
            {
                Console.WriteLine($"  Subject:      {certificate.Subject}");
                Console.WriteLine($"  Issuer:       {certificate.Issuer}");
                Console.WriteLine($"  Thumbprint:   {certificate.Thumbprint}");
                Console.WriteLine($"  NotAfter:     {certificate.NotAfter:u}");
                Console.WriteLine($"  HasKey:       {certificate.HasPrivateKey}");
                Console.WriteLine();
            }

            var selection = CertificateSelector.Select(
                certificates,
                authority.Certificate,
                new CertificateSelectionCriteria
                {
                    Thumbprint = options.ClientCertificateThumbprint,
                });

            Console.WriteLine("Selection diagnostics:");
            foreach (var diagnostic in selection.Diagnostics)
            {
                Console.WriteLine($"  {diagnostic}");
            }

            if (selection.Selected is not null)
            {
                Console.WriteLine();
                Console.WriteLine($"Selected: {selection.Selected.Subject} [{selection.Selected.Thumbprint}]");
            }
            return selection.Selected is null ? 2 : 0;
        }
        finally
        {
            foreach (var certificate in certificates)
            {
                certificate.Dispose();
            }
        }
    }

    private static void PrintItem(SyncItem item)
    {
        if (item.PayloadType == PayloadTypes.Text)
        {
            Console.WriteLine(
                $"[TEXT] {ShortId(item.SenderDeviceId)}: {Truncate(item.Text ?? string.Empty, 100)}");
            return;
        }

        var meta = item.File;
        Console.WriteLine(
            $"[{item.PayloadType.ToUpperInvariant()}] {Display(meta?.FileName ?? "?")} " +
            $"({FormatBytes(meta?.FileSize ?? 0)}) from {ShortId(item.SenderDeviceId)} " +
            $"[placeholder, record {item.RecordId}; type: download {item.RecordId}]");
    }

    private static void PrintUpdate(SyncItem item)
    {
        if (item.State == SyncItemState.Downloaded)
        {
            Console.WriteLine($"[DONE] {Display(item.File?.FileName ?? "?")} -> {Display(item.LocalPath ?? "?")}");
        }
        else if (item.State == SyncItemState.Failed)
        {
            Console.WriteLine($"[FAIL] {Display(item.File?.FileName ?? "?")}: {Display(item.Error ?? "?")}");
        }
    }

    private static void PrintList(SyncItemStore store)
    {
        var items = store.Snapshot();
        if (items.Count == 0)
        {
            Console.WriteLine("(no sync history)");
            return;
        }

        foreach (var item in items)
        {
            var label = item.PayloadType == PayloadTypes.Text
                ? Truncate(item.Text ?? string.Empty, 40)
                : $"{Display(item.File?.FileName ?? "?")} ({FormatBytes(item.File?.FileSize ?? 0)})";
            Console.WriteLine($"  {item.RecordId}  {item.PayloadType,-5}  {item.State,-11}  {label}");
        }
    }

    private static void PrintHelp()
    {
        Console.WriteLine();
        Console.WriteLine("commands:");
        Console.WriteLine("  list                 show sync history");
        Console.WriteLine("  download <recordId>  download a file/image placeholder with Range resume");
        Console.WriteLine("  help                 show this help");
        Console.WriteLine("  quit                 exit");
        Console.WriteLine();
    }

    private static void PrintUsage()
    {
        Console.WriteLine("MoliSync Windows client");
        Console.WriteLine("  MoliSync.Client.exe [--config <path>] [--headless] [--list-certificates]");
        Console.WriteLine();
        Console.WriteLine("  --config <path>       client JSON configuration (default: client.json next to the exe)");
        Console.WriteLine("  --headless            no interactive commands; run until Ctrl+C");
        Console.WriteLine("  --list-certificates   print CurrentUser\\MY certificates and selection diagnostics");
    }

    private static string ShortId(string value) =>
        string.IsNullOrEmpty(value) ? "?" : value.Length <= 12 ? value : value[..12];

    private static string Truncate(string value, int limit)
    {
        var singleLine = Display(value);
        return singleLine.Length <= limit ? singleLine : singleLine[..limit] + "...";
    }

    private static string Display(string value) =>
        string.Create(value.Length, value, static (span, source) =>
        {
            for (var index = 0; index < source.Length; index++)
            {
                span[index] = char.IsControl(source[index]) ? ' ' : source[index];
            }
        });

    private static string FormatBytes(long bytes) => bytes switch
    {
        < 1024 => $"{bytes} B",
        < 1024 * 1024 => $"{bytes / 1024.0:F1} KiB",
        < 1024L * 1024 * 1024 => $"{bytes / (1024.0 * 1024):F1} MiB",
        _ => $"{bytes / (1024.0 * 1024 * 1024):F2} GiB",
    };
}
