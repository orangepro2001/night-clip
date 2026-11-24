using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media.Imaging;
using Avalonia.Platform.Storage;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using System.Runtime.InteropServices;
using SkiaSharp;
using ZXing;
using ZXing.Common;

namespace MoonPassword.Windows.Views;

internal static class OtpTransferWindow
{
    private const string ImportTitle = "添加验证码（OTP）";
    private const string ExportTitle = "导出与迁移验证码";
    internal static readonly FilePickerFileType Images = new("二维码图片") { Patterns = ["*.png", "*.jpg", "*.jpeg", "*.bmp", "*.webp"] };
    private static readonly FilePickerFileType TextFile = new("OTP 链接文件") { Patterns = ["*.txt"] };

    public static async Task<bool> ImportAsync(Window? owner, ShellController shell, string groupId)
    {
        var links = new TextBox { AcceptsReturn = true, PasswordChar = '•', IsUndoEnabled = false, MinHeight = 100, MaxLength = 4 << 20,
            PlaceholderText = "粘贴 otpauth:// 或 otpauth-migration:// 链接，每行一个" };
        var error = Ui.Text("", "caption", "danger");
        var count = Ui.Text("", "caption", "secondary");
        var scan = DialogKit.Btn("扫描图片");
        var open = DialogKit.Btn("打开链接文件");
        var import = DialogKit.Btn("导入", 88, "primary");
        var cancel = DialogKit.Btn(Strings.BtnCancel, 88);
        var panel = new StackPanel
        {
            Spacing = 12,
            Children = {
                Ui.Text(ImportTitle, "title"),
                Ui.Text("支持普通验证码和 Google Authenticator 迁移。多张迁移二维码请全部加入，再点导入。", "secondary"),
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { scan, open } },
                links, count, error, DialogKit.ButtonRow(cancel, import),
            },
        };
        var w = DialogKit.Create(shell, ImportTitle, 540, panel);
        bool closed = false, added = false;
        w.Closed += (_, _) => { closed = true; links.Clear(); };
        cancel.Click += (_, _) => w.Close();
        string[] Pending() => (links.Text ?? "").TrimStart('\uFEFF').Split('\n', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries);
        links.TextChanged += (_, _) => count.Text = $"已添加 {Pending().Length} 条链接";
        async Task AddFileAsync(bool image)
        {
            error.Text = "";
            var path = await VaultUi.PickOpenFileAsync(w, ImportTitle, image ? Images : TextFile);
            if (path is null || closed) return;
            try
            {
                var content = await Task.Run(() => image ? ReadQr(path) : ReadLinks(path));
                if (closed) return;
                if (content.Length == 0) { error.Text = "图片中没有找到验证码二维码。"; return; }
                if ((links.Text?.Length ?? 0) + content.Length + 1 > links.MaxLength)
                {
                    error.Text = "链接文件过大。"; return;
                }
                links.Text = string.IsNullOrWhiteSpace(links.Text) ? content : links.Text.TrimEnd() + "\n" + content;
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ArgumentException)
            {
                if (!closed) error.Text = "无法读取所选文件，请检查文件格式和大小。";
            }
        }
        scan.Click += async (_, _) => await AddFileAsync(true);
        open.Click += async (_, _) => await AddFileAsync(false);
        import.Click += async (_, _) =>
        {
            var p = Pending();
            if (p.Length == 0) { error.Text = "请先扫描二维码或粘贴链接。"; return; }
            import.IsEnabled = scan.IsEnabled = open.IsEnabled = links.IsEnabled = false;
            error.Text = "";
            var result = await shell.VaultCallAsync(a => a.ImportOtp(p, groupId));
            if (closed) return;
            import.IsEnabled = scan.IsEnabled = open.IsEnabled = links.IsEnabled = true;
            if (!result.Ok) { error.Text = result.Error; return; }
            var (n, duplicates, unsupported) = result.Value;
            added = n > 0;
            links.Clear();
            await VaultUi.InfoAsync(w, $"已添加 {n} 项，跳过重复 {duplicates} 项，不支持的验证码 {unsupported} 项。", ImportTitle);
            if (!closed) w.Close();
        };
        await w.ShowModalAsync(owner);
        return added;
    }

    public static async Task ExportAsync(Window? owner, ShellController shell, string? id = null)
    {
        var creds = new CredsBox { Caption = Strings.ExportMasterPrompt };
        var confirm = DialogKit.Btn("验证并查看", 100, "primary");
        var done = DialogKit.Btn("关闭", 80);
        var copy = DialogKit.Btn("复制所选链接");
        var save = DialogKit.Btn(id is null ? "导出所有链接…" : "导出链接…");
        var qr = new Image { Width = 248, Height = 248, HorizontalAlignment = HorizontalAlignment.Center };
        var entries = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { copy, save } };
        var preview = new StackPanel { Spacing = 10, IsVisible = false, Children = { entries, qr, actions } };
        var message = Ui.Text("", "caption", "secondary");
        var panel = new StackPanel { Spacing = 12, Children = {
            Ui.Text(ExportTitle, "title"),
            Ui.Text("二维码和导出文件含验证码密钥。请只交给你信任的设备或应用，迁移后妥善保管或删除文件。", "secondary"),
            creds, preview, message, DialogKit.ButtonRow(done, confirm),
        } };
        var w = DialogKit.Create(shell, ExportTitle, 520, panel);
        bool closed = false;
        Bitmap? qrBitmap = null;
        OtpExportItem[] items = [];
        done.Click += (_, _) => w.Close();
        entries.SelectionChanged += (_, _) =>
        {
            qr.Source = null;
            qrBitmap?.Dispose();
            qrBitmap = null;
            if (entries.SelectedItem is not OtpExportItem item) return;
            qrBitmap = CreateQr(item.Link);
            qr.Source = qrBitmap;
            qr.IsVisible = qrBitmap is not null;
        };
        w.Closed += (_, _) =>
        {
            closed = true;
            creds.Clear(keyFileToo: true);
            qr.Source = null;
            qrBitmap?.Dispose();
            qrBitmap = null;
            items = [];
            entries.ItemsSource = null;
        };
        confirm.Click += async (_, _) =>
        {
            if (creds.Password.Length == 0) { creds.FocusPassword(); return; }
            confirm.IsEnabled = false;
            var credentials = creds.Creds;
            creds.Clear();
            var r = await shell.VaultCallAsync(a => a.ExportOtp(credentials, id));
            if (closed) return;
            confirm.IsEnabled = true;
            if (!r.Ok) { message.Text = r.Error; return; }
            (items, var skipped) = r.Value;
            creds.IsVisible = confirm.IsVisible = false;
            message.Text = items.Length == 0 ? "密码库中没有可导出的验证码。" :
                skipped == 0 ? "用另一款验证器扫描当前二维码，或导出文本文件在 MoonPassword 中批量导入。" :
                $"已找到 {items.Length} 项，另有 {skipped} 项无法导出。";
            preview.IsVisible = items.Length > 0;
            entries.ItemsSource = items;
            entries.SelectedIndex = items.Length > 0 ? 0 : -1;
        };
        copy.Click += async (_, _) =>
        {
            if (entries.SelectedItem is OtpExportItem item)
                message.Text = await shell.CopySensitiveTextAsync(item.Link, "验证码链接", 30) ?? "链接已复制，30 秒后自动清除。";
        };
        save.Click += async (_, _) =>
        {
            var path = await VaultUi.PickSaveFileAsync(w, ExportTitle, "moonpassword-otp.txt", TextFile);
            if (path is null || closed || items.Length == 0) return;
            try
            {
                using var f = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None);
                using var writer = new StreamWriter(f);
                foreach (var item in items) writer.WriteLine(item.Link);
                message.Text = "已导出验证码链接文件。";
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                message.Text = "无法写入导出文件。";
            }
        };
        await w.ShowModalAsync(owner);
    }

    internal static async Task AddQrToEntryAsync(Window owner, Action<string> apply)
    {
        var path = await VaultUi.PickOpenFileAsync(owner, ImportTitle, Images);
        if (path is null || !owner.IsVisible) return;
        string link;
        try { link = await Task.Run(() => ReadQr(path)); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ArgumentException) { link = ""; }
        if (!owner.IsVisible) return;
        if (link.StartsWith("otpauth://totp/", StringComparison.OrdinalIgnoreCase)) apply(link);
        else await VaultUi.ErrorAsync(owner, "请选择含一条 TOTP 链接的二维码。多个账号请从「添加验证码」导入。");
    }

    private static string ReadLinks(string path)
    {
        using var f = File.OpenRead(path);
        if (f.Length > 4 << 20) throw new IOException();
        using var reader = new StreamReader(f);
        return reader.ReadToEnd();
    }

    internal static string ReadQr(string path)
    {
        using var f = File.OpenRead(path);
        if (f.Length > 32 << 20) throw new IOException();
        using var codec = SKCodec.Create(f);
        if (codec is null || codec.Info.Width <= 0 || codec.Info.Height <= 0 ||
            (long)codec.Info.Width * codec.Info.Height > 16_000_000) return "";
        using var bitmap = new SKBitmap(new SKImageInfo(codec.Info.Width, codec.Info.Height, SKColorType.Bgra8888, SKAlphaType.Opaque));
        if (codec.GetPixels(bitmap.Info, bitmap.GetPixels()) != SKCodecResult.Success) return "";
        var reader = new BarcodeReaderGeneric { AutoRotate = true };
        reader.Options.PossibleFormats = [BarcodeFormat.QR_CODE];
        reader.Options.TryHarder = true;
        reader.Options.TryInverted = true;
        return reader.Decode(bitmap.Bytes, bitmap.Width, bitmap.Height, RGBLuminanceSource.BitmapFormat.BGRA32)?.Text ?? "";
    }

    private static Bitmap? CreateQr(string link)
    {
        try
        {
            var encoder = new BarcodeWriterPixelData { Format = BarcodeFormat.QR_CODE, Options = new EncodingOptions { Width = 496, Height = 496, Margin = 4 } };
            var data = encoder.Write(link);
            using var bitmap = new SKBitmap(new SKImageInfo(data.Width, data.Height, SKColorType.Bgra8888, SKAlphaType.Opaque));
            Marshal.Copy(data.Pixels, 0, bitmap.GetPixels(), data.Pixels.Length);
            using var png = bitmap.Encode(SKEncodedImageFormat.Png, 100).AsStream();
            return new Bitmap(png);
        }
        catch (WriterException) { return null; }
    }
}
