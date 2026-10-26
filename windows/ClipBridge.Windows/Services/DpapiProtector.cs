using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Services;

/// <summary>
/// Windows DPAPI, current-user scope, never any UI (CRYPTPROTECT_UI_FORBIDDEN). A fixed application
/// entropy string is mixed in so another program running as the same user cannot decrypt the file by
/// simply calling CryptUnprotectData on it.
/// </summary>
internal sealed class DpapiProtector : IKeyProtector
{
    private static readonly byte[] Entropy = Encoding.UTF8.GetBytes("ClipBridge.devkey.v1");

    public byte[] Protect(byte[] plain) => Transform(plain, protect: true);

    public byte[] Unprotect(byte[] protectedBlob) => Transform(protectedBlob, protect: false);

    private static byte[] Transform(byte[] input, bool protect)
    {
        var inPtr = IntPtr.Zero;
        var entPtr = IntPtr.Zero;
        var output = new NativeMethods.DATA_BLOB();
        try
        {
            inPtr = Marshal.AllocHGlobal(Math.Max(1, input.Length));
            Marshal.Copy(input, 0, inPtr, input.Length);
            entPtr = Marshal.AllocHGlobal(Entropy.Length);
            Marshal.Copy(Entropy, 0, entPtr, Entropy.Length);

            var inBlob = new NativeMethods.DATA_BLOB { cbData = input.Length, pbData = inPtr };
            var entBlob = new NativeMethods.DATA_BLOB { cbData = Entropy.Length, pbData = entPtr };

            var ok = protect
                ? NativeMethods.CryptProtectData(ref inBlob, null, ref entBlob, IntPtr.Zero, IntPtr.Zero,
                    NativeMethods.CRYPTPROTECT_UI_FORBIDDEN, out output)
                : NativeMethods.CryptUnprotectData(ref inBlob, IntPtr.Zero, ref entBlob, IntPtr.Zero, IntPtr.Zero,
                    NativeMethods.CRYPTPROTECT_UI_FORBIDDEN, out output);
            if (!ok)
                throw new CryptographicException(new Win32Exception(Marshal.GetLastWin32Error()).Message);

            var result = new byte[output.cbData];
            Marshal.Copy(output.pbData, result, 0, output.cbData);
            return result;
        }
        finally
        {
            // Scrub what we allocated for the input: it may hold the plaintext key.
            if (inPtr != IntPtr.Zero)
            {
                if (input.Length > 0) Marshal.Copy(new byte[input.Length], 0, inPtr, input.Length);
                Marshal.FreeHGlobal(inPtr);
            }
            if (entPtr != IntPtr.Zero) Marshal.FreeHGlobal(entPtr);
            if (output.pbData != IntPtr.Zero)
            {
                if (output.cbData > 0) Marshal.Copy(new byte[output.cbData], 0, output.pbData, output.cbData);
                NativeMethods.LocalFree(output.pbData); // DPAPI allocates the output with LocalAlloc
            }
        }
    }
}
