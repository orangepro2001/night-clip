using System.Buffers.Binary;
using System.IO.Pipes;
using System.Security;
using System.Text.Json;
using Avalonia.Controls;
using Avalonia.Threading;
using Microsoft.Win32;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Views;

namespace MoonPassword.Windows.Services;

/// <summary>Firefox's native host relays one request to the running desktop vault over a current-user pipe.</summary>
internal sealed class FirefoxBridge(ShellController shell) : IDisposable
{
    private const string Host = "cloud.moonl.moonpassword";
    private const string AddonId = "moonpassword@moonl.cloud";
    private const string RegistryPath = @"Software\Mozilla\NativeMessagingHosts\" + Host;
    private static string ManifestPath => Path.Combine(App.DataDir, Host + ".json");
    private static string PipeName => "MoonPassword.Firefox" + App.InstanceSuffix;
    private static readonly TimeSpan ApprovalTime = TimeSpan.FromSeconds(45);
    private const int MaxMessage = 128 * 1024;
    private readonly CancellationTokenSource _stop = new();

    public static bool IsRegistered
    {
        get
        {
            try
            {
                using var key = Registry.CurrentUser.OpenSubKey(RegistryPath);
                return SamePath(key?.GetValue(null) as string, ManifestPath);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException) { return false; }
        }
    }

    private static bool SamePath(string? a, string b) =>
        string.Equals(a, b, StringComparison.OrdinalIgnoreCase);

    public static void Register()
    {
        using var old = Registry.CurrentUser.OpenSubKey(RegistryPath);
        if (old?.GetValue(null) is string other && !SamePath(other, ManifestPath))
            throw new InvalidOperationException();
        Directory.CreateDirectory(App.DataDir);
        var manifest = JsonSerializer.Serialize(new
        {
            name = Host,
            description = "MoonPassword Firefox bridge",
            path = Environment.ProcessPath ?? throw new InvalidOperationException(),
            type = "stdio",
            allowed_extensions = new[] { AddonId },
        });
        File.WriteAllText(ManifestPath, manifest);
        using var key = Registry.CurrentUser.CreateSubKey(RegistryPath);
        key.SetValue("", ManifestPath);
    }

    public static void Unregister()
    {
        if (IsRegistered) Registry.CurrentUser.DeleteSubKey(RegistryPath, throwOnMissingSubKey: false);
    }

    public static bool IsNativeInvocation(string[] args) =>
        args.Length >= 2 && SamePath(args[0], ManifestPath) && args[1] == AddonId;

    public static async Task RelayAsync()
    {
        using var input = Console.OpenStandardInput();
        using var output = Console.OpenStandardOutput();
        using var deadline = new CancellationTokenSource(ApprovalTime + TimeSpan.FromSeconds(5));
        var ct = deadline.Token;
        object response;
        try
        {
            var request = await ReadAsync(input, ct);
            using var pipe = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut, PipeOptions.CurrentUserOnly | PipeOptions.Asynchronous);
            await pipe.ConnectAsync(1500, ct);
            await WriteAsync(pipe, request, ct);
            response = await ReadAsync(pipe, ct);
        }
        catch (Exception e) when (e is IOException or JsonException or OperationCanceledException or TimeoutException or UnauthorizedAccessException)
        {
            response = Failure("APP_UNAVAILABLE");
        }
        try { await WriteAsync(output, response, CancellationToken.None); }
        catch (IOException) { }
    }

    public void Start()
    {
        var ct = _stop.Token;
        _ = Task.Run(() => ListenAsync(ct));
    }

    public void Dispose()
    {
        try { _stop.Cancel(); }
        catch (ObjectDisposedException) { }
    }

    private async Task ListenAsync(CancellationToken stop)
    {
        try
        {
            while (!stop.IsCancellationRequested)
            {
                using var pipe = new NamedPipeServerStream(PipeName, PipeDirection.InOut, 1, PipeTransmissionMode.Byte,
                    PipeOptions.CurrentUserOnly | PipeOptions.Asynchronous);
                await pipe.WaitForConnectionAsync(stop);
                using var requestTimeout = CancellationTokenSource.CreateLinkedTokenSource(stop);
                requestTimeout.CancelAfter(TimeSpan.FromSeconds(4));
                try
                {
                    var request = await ReadAsync(pipe, requestTimeout.Token);
                    requestTimeout.CancelAfter(ApprovalTime);
                    var response = await HandleAsync(request, requestTimeout.Token);
                    using var writeTimeout = CancellationTokenSource.CreateLinkedTokenSource(stop);
                    writeTimeout.CancelAfter(TimeSpan.FromSeconds(3));
                    await WriteAsync(pipe, response, writeTimeout.Token);
                }
                catch (Exception e) when (e is IOException or JsonException or OperationCanceledException) { }
            }
        }
        catch (OperationCanceledException) when (stop.IsCancellationRequested) { }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            AppLog.Write("Firefox bridge could not open the local pipe");
        }
        finally { _stop.Dispose(); }
    }

    private static async Task<JsonElement> ReadAsync(Stream stream, CancellationToken ct)
    {
        var header = new byte[4];
        await stream.ReadExactlyAsync(header, ct);
        int length = BinaryPrimitives.ReadInt32LittleEndian(header);
        if (length <= 0 || length > MaxMessage) throw new IOException();
        var bytes = new byte[length];
        await stream.ReadExactlyAsync(bytes, ct);
        using var doc = JsonDocument.Parse(bytes, new JsonDocumentOptions { MaxDepth = 8 });
        return doc.RootElement.Clone();
    }

    private static async Task WriteAsync(Stream stream, object message, CancellationToken ct)
    {
        var bytes = JsonSerializer.SerializeToUtf8Bytes(message);
        if (bytes.Length > MaxMessage) bytes = JsonSerializer.SerializeToUtf8Bytes(Failure("MESSAGE_TOO_LARGE"));
        var header = new byte[4];
        BinaryPrimitives.WriteInt32LittleEndian(header, bytes.Length);
        await stream.WriteAsync(header, ct);
        await stream.WriteAsync(bytes, ct);
        await stream.FlushAsync(ct);
    }

    private static object Failure(string error) => new { ok = false, error };

    private async Task<object> HandleAsync(JsonElement p, CancellationToken ct)
    {
        if (!IsRegistered) return Failure("DISABLED");
        var action = JsonRead.Str(p, "action");
        if (action == "open")
        {
            await Dispatcher.UIThread.InvokeAsync(() => shell.ShowMainWindow(MainPage.Vault), DispatcherPriority.Normal, ct);
            return new { ok = true };
        }
        var rawOrigin = JsonRead.Str(p, "origin");
        if (rawOrigin.Length > 512 || !Uri.TryCreate(rawOrigin, UriKind.Absolute, out var site) || site.UserInfo.Length > 0 ||
            (site.Scheme != Uri.UriSchemeHttps && !(site.Scheme == Uri.UriSchemeHttp && site.IsLoopback)))
            return Failure("INVALID_ORIGIN");
        var origin = site.GetComponents(UriComponents.SchemeAndServer, UriFormat.UriEscaped);
        try
        {
            switch (action)
            {
                case "match":
                {
                    var result = await shell.SessionCallAsync("vault.browser_logins", new { origin });
                    var matches = JsonRead.Arr(result, "matches").Take(50).Select(m => new
                    {
                        id = JsonRead.Str(m, "id"),
                        title = Limit(JsonRead.Str(m, "title")),
                        username = Limit(JsonRead.Str(m, "username")),
                    }).ToArray();
                    return new { ok = true, matches };
                }
                case "fill":
                {
                    var id = JsonRead.Str(p, "id");
                    if (id.Length != 32 || !id.All(Uri.IsHexDigit)) return Failure("INVALID_ENTRY");
                    var candidates = await shell.SessionCallAsync("vault.browser_logins", new { origin });
                    var entry = JsonRead.Arr(candidates, "matches").FirstOrDefault(e => JsonRead.Str(e, "id") == id);
                    if (entry.ValueKind == JsonValueKind.Undefined) return Failure("NO_MATCH");
                    var title = Limit(JsonRead.Str(entry, "title"));
                    var username = Limit(JsonRead.Str(entry, "username"));
                    if (!await ApproveAsync("允许 Firefox 填充密码？", origin, title, username, "允许一次", ct))
                        return Failure("CANCELLED");
                    if (!IsRegistered) return Failure("DISABLED");
                    var current = await shell.SessionCallAsync("vault.browser_logins", new { origin, id });
                    var match = JsonRead.Arr(current, "matches").FirstOrDefault();
                    if (match.ValueKind == JsonValueKind.Undefined ||
                        JsonRead.Str(match, "title") != JsonRead.Str(entry, "title") ||
                        JsonRead.Str(match, "username") != JsonRead.Str(entry, "username")) return Failure("NO_MATCH");
                    ct.ThrowIfCancellationRequested();
                    return new { ok = true, username = JsonRead.Str(match, "username"), password = JsonRead.Str(match, "password") };
                }
                case "save":
                {
                    var username = JsonRead.Str(p, "username");
                    var password = JsonRead.Str(p, "password");
                    if (username.Length > 512 || password.Length is < 1 or > 1024) return Failure("INVALID_ENTRY");
                    await shell.SessionCallAsync("vault.browser_logins", new { origin });
                    if (!await ApproveAsync("允许 Firefox 添加登录条目？", origin, site.IdnHost, Limit(username), "保存新条目", ct))
                        return Failure("CANCELLED");
                    if (!IsRegistered) return Failure("DISABLED");
                    ct.ThrowIfCancellationRequested();
                    await shell.SessionCallAsync("vault.add", new
                    {
                        entry = new { kind = "login", title = site.IdnHost, url = origin, username, password },
                    });
                    return new { ok = true };
                }
                default: return Failure("UNKNOWN_ACTION");
            }
        }
        catch (CoreException e) { return Failure(e.Code); }
    }

    private static string Limit(string value)
    {
        var text = new string(value.Where(c => !char.IsControl(c) && char.GetUnicodeCategory(c) != System.Globalization.UnicodeCategory.Format).Take(180).ToArray());
        return text;
    }

    private async Task<bool> ApproveAsync(string title, string origin, string name, string username, string verb, CancellationToken ct)
    {
        var task = await Dispatcher.UIThread.InvokeAsync(async () =>
        {
            if (ct.IsCancellationRequested || !shell.VaultUnlocked) return false;
            var no = DialogKit.Btn(Strings.BtnCancel, 88);
            var yes = DialogKit.Btn(verb, 112, "primary");
            var body = new StackPanel { Spacing = 12, Children = {
                Ui.Text(title, "title"), Ui.Text(origin, "mono"), Ui.Text(name, "secondary"),
                new TextBlock { Text = username, IsVisible = username.Length > 0 }, DialogKit.ButtonRow(no, yes),
            } };
            var window = DialogKit.Create(shell, title, 470, body);
            window.Topmost = true;
            bool approved = false;
            no.Click += (_, _) => window.Close();
            yes.Click += (_, _) => { approved = true; window.Close(); };
            using var expiry = ct.Register(() => Dispatcher.UIThread.Post(() => { if (window.IsVisible) window.Close(); }));
            await window.ShowModalAsync(shell.Main);
            return approved && !ct.IsCancellationRequested && shell.VaultUnlocked;
        }, DispatcherPriority.Normal, ct);
        return await task;
    }
}
