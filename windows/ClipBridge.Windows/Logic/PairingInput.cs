using System.Buffers.Text;
using System.Globalization;
using System.Net;
using System.Net.Sockets;

namespace ClipBridge.Windows.Logic;

/// <summary>Arguments for <c>pair.join</c>: either a link, or host/port/psk(/fp).</summary>
public sealed record PairJoinRequest(string? Link, string? Host, int Port, string? Psk, string? Fp)
{
    public Dictionary<string, object> ToParams()
    {
        if (Link is not null) return new Dictionary<string, object> { ["link"] = Link };
        var p = new Dictionary<string, object> { ["host"] = Host ?? "", ["port"] = Port, ["psk"] = Psk ?? "" };
        if (!string.IsNullOrEmpty(Fp)) p["fp"] = Fp;
        return p;
    }
}

public sealed record PairInputResult(PairJoinRequest? Request, string? Error)
{
    public bool Ok => Request is not null;
}

/// <summary>
/// Client-side validation of a pairing link / manual entry, mirroring the core's strict parser
/// (core/pairing/codec.go ParseLink) so that mistakes are reported before any network call.
/// The core stays the authority; this only gives faster, friendlier errors. Secrets are never echoed.
/// </summary>
public static class PairingInput
{
    public const string Scheme = "clipbridge://pair";
    public const int PskBytes = 16;

    private static readonly System.Text.RegularExpressions.Regex StrictIp = new(
        @"^(?:\d{1,3}(?:\.\d{1,3}){3}|[0-9A-Fa-f:]*:[0-9A-Fa-f:.]*)$", System.Text.RegularExpressions.RegexOptions.Compiled);
    public const int FingerprintChars = 43;
    public const int FingerprintBytes = 32;

    /// <summary>Validates pasted text (it may contain surrounding text; the first clipbridge:// token is used).</summary>
    public static PairInputResult FromLink(string? text, DateTimeOffset now)
    {
        var link = ExtractLink(text);
        if (link is null) return Fail(Strings.PairLinkNotRecognised);

        var rest = link[Scheme.Length..];
        if (rest.StartsWith('/')) rest = rest[1..];
        if (!rest.StartsWith('?')) return Fail(Strings.PairLinkNotRecognised);
        var query = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var part in rest[1..].Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var eq = part.IndexOf('=');
            var key = eq < 0 ? part : part[..eq];
            string value;
            try { value = eq < 0 ? "" : Uri.UnescapeDataString(part[(eq + 1)..]); }
            catch (UriFormatException) { return Fail(Strings.PairLinkNotRecognised); }
            if (!query.TryAdd(key, value)) return Fail(Strings.PairLinkNotRecognised); // duplicated parameter
        }

        if (!query.TryGetValue("v", out var v) || v != "1") return Fail(Strings.PairLinkVersion);
        if (!query.TryGetValue("host", out var host)) return Fail(Strings.PairLinkNotRecognised);
        if (!query.TryGetValue("port", out var portText)) return Fail(Strings.PairLinkNotRecognised);
        if (!query.TryGetValue("psk", out var psk)) return Fail(Strings.PairLinkNotRecognised);
        query.TryGetValue("fp", out var fp);
        if (!query.TryGetValue("exp", out var expText)
            || !long.TryParse(expText, NumberStyles.None, CultureInfo.InvariantCulture, out var exp))
            return Fail(Strings.PairLinkNotRecognised);
        if (exp <= now.ToUnixTimeSeconds()) return Fail(Strings.ErrPairingExpired);

        var manual = Validate(host, portText, psk, fp);
        if (!manual.Ok) return manual;
        // Send the normalised link text to the core (not the surrounding paragraph).
        return new PairInputResult(new PairJoinRequest(link, null, 0, null, null), null);
    }

    /// <summary>Validates manually typed host/port/psk and optional fingerprint.</summary>
    public static PairInputResult FromManual(string? host, string? port, string? psk, string? fp) =>
        Validate((host ?? "").Trim(), (port ?? "").Trim(), (psk ?? "").Trim(), string.IsNullOrWhiteSpace(fp) ? null : fp.Trim());

    private static PairInputResult Validate(string host, string portText, string psk, string? fp)
    {
        // IPAddress.TryParse is lenient ("100.101.146" is accepted as 100.101.0.146); the core is not.
        if (!StrictIp.IsMatch(host) || !IPAddress.TryParse(host, out var addr))
            return Fail(Strings.PairHostNotIp);
        if (!IsTailnetAddress(addr)) return Fail(Strings.PairHostNotTailnet);
        if (!int.TryParse(portText, NumberStyles.None, CultureInfo.InvariantCulture, out var port) || port < 1 || port > 65535)
            return Fail(Strings.PairBadPort);
        if (!IsBase64Url(psk, PskBytes)) return Fail(Strings.PairBadPsk);
        if (fp is not null && (fp.Length != FingerprintChars || !IsBase64Url(fp, FingerprintBytes)))
            return Fail(Strings.PairBadFp);
        return new PairInputResult(new PairJoinRequest(null, addr.ToString(), port, psk, fp), null);
    }

    /// <summary>100.64.0.0/10 or fd7a:115c:a1e0::/48 (a v4-mapped v6 address is unwrapped first).</summary>
    public static bool IsTailnetAddress(IPAddress addr)
    {
        if (addr.IsIPv4MappedToIPv6) addr = addr.MapToIPv4();
        var b = addr.GetAddressBytes();
        if (addr.AddressFamily == AddressFamily.InterNetwork)
            return b[0] == 100 && (b[1] & 0xC0) == 0x40;
        return addr.AddressFamily == AddressFamily.InterNetworkV6
               && b[0] == 0xfd && b[1] == 0x7a && b[2] == 0x11 && b[3] == 0x5c && b[4] == 0xa1 && b[5] == 0xe0;
    }

    /// <summary>Unpadded URL-safe base64 that decodes to exactly <paramref name="bytes"/> bytes.</summary>
    public static bool IsBase64Url(string s, int bytes)
    {
        if (s.Length == 0 || !Base64Url.IsValid(s, out var decoded)) return false;
        return decoded == bytes;
    }

    /// <summary>Finds the first "clipbridge://pair…" token in pasted text.</summary>
    public static string? ExtractLink(string? text)
    {
        if (string.IsNullOrWhiteSpace(text)) return null;
        var i = text.IndexOf(Scheme, StringComparison.OrdinalIgnoreCase);
        if (i < 0) return null;
        var j = i;
        while (j < text.Length && !char.IsWhiteSpace(text[j]) && text[j] != '"' && text[j] != '\'' && text[j] != '<' && text[j] != '>') j++;
        var link = text[i..j];
        // Normalise the scheme/host case only; the query is case-sensitive.
        return Scheme + link[Scheme.Length..];
    }

    private static PairInputResult Fail(string error) => new(null, error);
}
