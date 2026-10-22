namespace MoliSync.Client.Services;

public static class MimeTypes
{
    private static readonly Dictionary<string, string> ByExtension = new(StringComparer.OrdinalIgnoreCase)
    {
        [".jpg"] = "image/jpeg",
        [".jpeg"] = "image/jpeg",
        [".png"] = "image/png",
        [".gif"] = "image/gif",
        [".webp"] = "image/webp",
        [".bmp"] = "image/bmp",
        [".svg"] = "image/svg+xml",
        [".heic"] = "image/heic",
        [".pdf"] = "application/pdf",
        [".txt"] = "text/plain",
        [".md"] = "text/markdown",
        [".json"] = "application/json",
        [".zip"] = "application/zip",
        [".7z"] = "application/x-7z-compressed",
        [".rar"] = "application/vnd.rar",
        [".doc"] = "application/msword",
        [".docx"] = "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        [".xls"] = "application/vnd.ms-excel",
        [".xlsx"] = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        [".ppt"] = "application/vnd.ms-powerpoint",
        [".pptx"] = "application/vnd.openxmlformats-officedocument.presentationml.presentation",
        [".mp4"] = "video/mp4",
        [".mov"] = "video/quicktime",
        [".mp3"] = "audio/mpeg",
        [".flac"] = "audio/flac",
        [".exe"] = "application/vnd.microsoft.portable-executable",
        [".apk"] = "application/vnd.android.package-archive",
    };

    public static string FromFileName(string fileName)
    {
        var extension = Path.GetExtension(fileName);
        return ByExtension.TryGetValue(extension, out var mime)
            ? mime
            : "application/octet-stream";
    }

    public static bool IsImage(string fileName) =>
        FromFileName(fileName).StartsWith("image/", StringComparison.OrdinalIgnoreCase);
}
