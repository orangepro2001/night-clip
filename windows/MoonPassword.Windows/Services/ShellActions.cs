using System.Diagnostics;
using Avalonia.Threading;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Update;

namespace MoonPassword.Windows.Services;

/// <summary>The actions the pages trigger. Every method returns an error text (null = success) instead of throwing.</summary>
internal sealed partial class ShellController
{
    // =====================================================================================
    //  Plain text to the clipboard (diagnostics, a public key)
    // =====================================================================================

    /// <summary>
    /// Copies app-generated, non-secret text through the clipboard worker (so it cannot overtake a pending
    /// "clear the copied password"). Secrets go through <see cref="CopyVaultSecretAsync"/> instead.
    /// </summary>
    public Task<bool> CopyTextAsync(string text)
    {
        var tcs = new TaskCompletionSource<bool>();
        _clipQueue.Post(() =>
        {
            var ok = false;
            if (!_disposed && !_uiShutdown)
            {
                try
                {
                    _ui.Invoke(() => ok = _clip.WriteText(_clipOwner.Handle, ClipText.ToClipboard(text)),
                        DispatcherPriority.Send, CancellationToken.None, TimeSpan.FromSeconds(3));
                }
                catch (Exception ex) when (ex is OperationCanceledException or TimeoutException) { }
            }
            tcs.TrySetResult(ok);
        });
        return tcs.Task;
    }

    public void SetNotifications(bool on)
    {
        Prefs.Notifications = on;
        Prefs.Save(App.DataDir);
    }

    // =====================================================================================
    //  Generic call helpers
    // =====================================================================================

    /// <summary>Runs a core call off the UI thread; returns the user-facing error text, or null on success.</summary>
    public async Task<string?> CallAsync(Action<CoreApi> call)
    {
        try
        {
            await Task.Run(() => call(Api));
            return null;
        }
        catch (CoreException ex)
        {
            AppLog.Write("core call failed: " + ex.Code);
            return ErrorMessages.ForCode(ex.Code);
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("core reply", ex);
            return ErrorMessages.ForCode("INTERNAL");
        }
    }

    /// <summary>CoreHost.Call parses the reply envelope itself; a broken one surfaces as one of these.</summary>
    private static bool IsMalformedReply(Exception ex) =>
        ex is System.Text.Json.JsonException or KeyNotFoundException or InvalidOperationException;

    public async Task<(T? Value, string? Error)> CallAsync<T>(Func<CoreApi, T> call)
    {
        try { return (await Task.Run(() => call(Api)), null); }
        catch (CoreException ex)
        {
            AppLog.Write("core call failed: " + ex.Code);
            return (default, ErrorMessages.ForCode(ex.Code));
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("core reply", ex);
            return (default, ErrorMessages.ForCode("INTERNAL"));
        }
    }

    // =====================================================================================
    //  Start-up
    // =====================================================================================

    public async Task<string?> SetAutoStartAsync(bool on)
    {
        // A development instance (own data folder) must not take over the installed app's Run entry.
        if (App.IsDevInstance) return Strings.DevInstanceNoAutoStart;
        var ok = await Task.Run(() => AutoStartRegistry.Set(on));
        _tray.SetAutoStartChecked(AutoStartRegistry.IsEnabled());
        StateChanged?.Invoke();
        return ok ? null : Strings.ErrInternal;
    }

    public bool AutoStartEnabled => AutoStartRegistry.IsEnabled();

    // =====================================================================================
    //  Updates
    // =====================================================================================

    public async Task<string?> UpdateActionAsync(UpdateInfo.Action action)
    {
        string? error = null;
        try
        {
            switch (action)
            {
                case UpdateInfo.Action.Check:
                    ShowStatus(Strings.UpdateChecking);
                    await Task.Run(() => Api.UpdateCheck()); // up to 30 s: never on the UI thread
                    break;
                case UpdateInfo.Action.Download:
                    await Task.Run(() => Api.UpdateDownload());
                    break;
                case UpdateInfo.Action.Install:
                    return await InstallUpdateAsync();
            }
        }
        catch (CoreException ex) { error = ErrorMessages.ForCode(ex.Code); }
        await RefreshUpdateAsync();
        return error;
    }

    public async Task<string?> SetAutoUpdateAsync(bool on)
    {
        var error = await CallAsync(a => a.UpdateConfigAuto(on));
        await RefreshUpdateAsync();
        return error;
    }

    /// <summary>Stages the verified package, starts the staged exe to swap the files and exits.</summary>
    private async Task<string?> InstallUpdateAsync()
    {
        try
        {
            var ready = Update.ReadyPath;
            var target = AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar);
            var pid = Environment.ProcessId;
            await Task.Run(() =>
            {
                var staged = UpdateApplier.Stage(ready, App.StagingDir);
                UpdateApplier.LaunchApplier(staged, target, pid);
            });
        }
        catch (Exception ex) when (ex is IOException or InvalidDataException or InvalidOperationException
                                       or UnauthorizedAccessException or System.ComponentModel.Win32Exception)
        {
            AppLog.Error("update install", ex);
            return Strings.UpdatePrepareFailed(ex.Message);
        }
        // The updater waits for this process to exit; OnExit disposes the core first.
        ExitApp();
        return null;
    }

    // =====================================================================================
    //  Folders and programs
    // =====================================================================================

    public string? OpenFolder(string dir)
    {
        try { Directory.CreateDirectory(dir); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { return Strings.FileMissing; }
        return Launch(new ProcessStartInfo("explorer.exe", "\"" + dir + "\"") { UseShellExecute = true });
    }

    private static string? Launch(ProcessStartInfo info)
    {
        try
        {
            using var p = Process.Start(info);
            return null;
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException or FileNotFoundException)
        {
            AppLog.Error("launch", ex);
            return ex.Message;
        }
    }
}
