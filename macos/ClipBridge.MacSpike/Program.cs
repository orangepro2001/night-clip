using Avalonia;

namespace ClipBridge.MacSpike;

internal static class Program
{
    [System.STAThread]
    public static void Main(string[] args) =>
        AppBuilder.Configure<App>().UsePlatformDetect().LogToTrace().StartWithClassicDesktopLifetime(args);
}
