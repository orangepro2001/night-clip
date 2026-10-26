using System.Reflection;

namespace ClipBridge.Windows.Update;

/// <summary>
/// Version and update-server values stamped into the assembly at build time
/// (<c>-p:CbVersionCode= -p:CbVersionName= -p:CbUpdateUrl=</c>, see Makefile
/// target dist-windows). A plain dev build has code 0, so updates stay off.
/// </summary>
public static class BuildInfo
{
    private static string Meta(string key) =>
        typeof(BuildInfo).Assembly.GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(a => a.Key == key)?.Value ?? "";

    public static long VersionCode => long.TryParse(Meta("CbVersionCode"), out var c) && c > 0 ? c : 0;
    public static string VersionName => Meta("CbVersionName") is { Length: > 0 } n ? n : "dev";
    public static string UpdateUrl => Meta("CbUpdateUrl");
}
