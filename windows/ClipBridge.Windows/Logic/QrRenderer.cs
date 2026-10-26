namespace ClipBridge.Windows.Logic;

/// <summary>
/// Turns <c>pair.begin</c>'s <c>qr.rows</c> (strings of "0"/"1", quiet zone included) into a bitmap
/// with nearest-neighbour scaling: no QR library and no interpolation, so modules stay crisp.
/// </summary>
public static class QrRenderer
{
    public const int MinModulePixels = 6;

    public sealed record Bitmap(int Width, int Height, byte[] Bgra32);

    /// <summary>Parses and validates the matrix; null when it is not a square of 0/1 characters.</summary>
    public static bool[,]? Parse(IReadOnlyList<string>? rows, int size)
    {
        if (rows is null || size < 1 || size > 512 || rows.Count != size) return null;
        var m = new bool[size, size];
        for (var y = 0; y < size; y++)
        {
            var row = rows[y];
            if (row.Length != size) return null;
            for (var x = 0; x < size; x++)
            {
                switch (row[x])
                {
                    case '1': m[y, x] = true; break;
                    case '0': break;
                    default: return null;
                }
            }
        }
        return m;
    }

    /// <summary>Integer pixels-per-module that fits <paramref name="targetPixels"/> but never drops below the minimum.</summary>
    public static int ChooseScale(int size, int targetPixels) =>
        Math.Max(MinModulePixels, size > 0 ? targetPixels / size : MinModulePixels);

    /// <summary>Black modules on white, BGRA, fully opaque. Dark modules are 1 in the matrix.</summary>
    public static Bitmap? Render(IReadOnlyList<string>? rows, int size, int targetPixels)
    {
        var m = Parse(rows, size);
        if (m is null) return null;
        var scale = ChooseScale(size, targetPixels);
        var px = size * scale;
        var data = new byte[px * px * 4];
        for (var y = 0; y < px; y++)
        {
            var my = y / scale;
            for (var x = 0; x < px; x++)
            {
                var v = m[my, x / scale] ? (byte)0x00 : (byte)0xFF;
                var i = (y * px + x) * 4;
                data[i] = v; data[i + 1] = v; data[i + 2] = v; data[i + 3] = 0xFF;
            }
        }
        return new Bitmap(px, px, data);
    }
}
