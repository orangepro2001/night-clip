using Avalonia.Threading;

namespace ClipBridge.Windows.Services;

/// <summary>A repeating UI-thread timer that is already running (WPF's callback constructor did the same).</summary>
internal static class UiTimer
{
    public static DispatcherTimer Start(TimeSpan interval, Action tick, DispatcherPriority? priority = null)
    {
        var timer = new DispatcherTimer(priority ?? DispatcherPriority.Background) { Interval = interval };
        timer.Tick += (_, _) => tick();
        timer.Start();
        return timer;
    }
}
