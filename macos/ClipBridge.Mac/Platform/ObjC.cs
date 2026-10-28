using System.Runtime.InteropServices;
using System.Text;

namespace ClipBridge.Mac.Platform;

/// <summary>
/// The few Objective-C runtime calls the shell needs (pasteboard, notifications), without bindings.
/// Every call that creates autoreleased objects must run inside <see cref="Pool"/>, because .NET
/// threads have no autorelease pool of their own.
/// </summary>
internal static class ObjC
{
    private const string Lib = "/usr/lib/libobjc.A.dylib";

    [DllImport(Lib)] public static extern IntPtr objc_getClass(string name);
    [DllImport(Lib)] public static extern IntPtr sel_registerName(string name);
    [DllImport(Lib)] private static extern IntPtr objc_autoreleasePoolPush();
    [DllImport(Lib)] private static extern void objc_autoreleasePoolPop(IntPtr pool);

    [DllImport(Lib, EntryPoint = "objc_msgSend")] public static extern IntPtr Send(IntPtr receiver, IntPtr selector);
    [DllImport(Lib, EntryPoint = "objc_msgSend")] public static extern IntPtr Send(IntPtr receiver, IntPtr selector, IntPtr a);
    [DllImport(Lib, EntryPoint = "objc_msgSend")] public static extern IntPtr Send(IntPtr receiver, IntPtr selector, IntPtr a, IntPtr b);
    [DllImport(Lib, EntryPoint = "objc_msgSend")] public static extern IntPtr SendLongArg(IntPtr receiver, IntPtr selector, long a);
    [DllImport(Lib, EntryPoint = "objc_msgSend")] public static extern long SendLong(IntPtr receiver, IntPtr selector);
    [DllImport(Lib, EntryPoint = "objc_msgSend")] public static extern bool SendBool(IntPtr receiver, IntPtr selector, IntPtr a, IntPtr b);
    [DllImport(Lib, EntryPoint = "objc_msgSend")] private static extern IntPtr SendBytes(IntPtr receiver, IntPtr selector, byte[] utf8);

    public static IntPtr Class(string name) => objc_getClass(name);
    public static IntPtr Sel(string name) => sel_registerName(name);

    /// <summary>Runs <paramref name="body"/> inside an autorelease pool.</summary>
    public static T Pool<T>(Func<T> body)
    {
        var pool = objc_autoreleasePoolPush();
        try { return body(); }
        finally { objc_autoreleasePoolPop(pool); }
    }

    /// <summary>An autoreleased NSString.</summary>
    public static IntPtr NSString(string s) =>
        SendBytes(Class("NSString"), Sel("stringWithUTF8String:"), Encoding.UTF8.GetBytes(s + "\0"));

    public static string? FromNSString(IntPtr str) =>
        str == IntPtr.Zero ? null : Marshal.PtrToStringUTF8(Send(str, Sel("UTF8String")));

    /// <summary>Whether an NSArray of NSStrings contains <paramref name="value"/>.</summary>
    public static bool ArrayContainsString(IntPtr array, string value) =>
        array != IntPtr.Zero && SendBool(array, Sel("containsObject:"), NSString(value), IntPtr.Zero);
}
