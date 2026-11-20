namespace MoonPassword.Windows.Logic;

/// <summary>
/// The MoonPassword mark (a crescent moon with a keyhole cut out) as polygons in a 24×24 design space,
/// x, y pairs. Fill <see cref="Crescent"/> and <see cref="Keyhole"/> together with the even-odd rule: the keyhole
/// lies inside the crescent and becomes a hole. Generated with the Android vectors and the macOS PNGs (docs/brand.md).
/// </summary>
public static class BrandMark
{
    /// <summary>Background gradient, top to bottom (#RRGGBB).</summary>
    public const string BackgroundTop = "#312E81";
    public const string BackgroundBottom = "#1E1B4B";

    /// <summary>Colour of the moon.</summary>
    public const string Moon = "#F8F4E3";

    public static readonly float[] Crescent =
    {
        11.93f, 3f, 11.38f, 3.03f, 10.83f, 3.1f, 10.29f, 3.2f, 9.76f, 3.33f, 9.23f, 3.5f,
        8.72f, 3.69f, 8.22f, 3.92f, 7.74f, 4.17f, 7.27f, 4.46f, 6.82f, 4.78f, 6.39f, 5.12f,
        5.98f, 5.48f, 5.59f, 5.87f, 5.23f, 6.29f, 4.89f, 6.72f, 4.58f, 7.18f, 4.31f, 7.65f,
        4.05f, 8.14f, 3.83f, 8.64f, 3.64f, 9.16f, 3.49f, 9.68f, 3.36f, 10.22f, 3.27f, 10.76f,
        3.21f, 11.31f, 3.19f, 11.86f, 3.19f, 12.4f, 3.24f, 12.95f, 3.31f, 13.5f, 3.42f, 14.04f,
        3.56f, 14.57f, 3.73f, 15.09f, 3.94f, 15.6f, 4.17f, 16.09f, 4.44f, 16.58f, 4.73f, 17.04f,
        5.05f, 17.49f, 5.4f, 17.91f, 5.77f, 18.31f, 6.17f, 18.69f, 6.59f, 19.05f, 7.03f, 19.38f,
        7.49f, 19.68f, 7.97f, 19.95f, 8.46f, 20.19f, 8.97f, 20.4f, 9.49f, 20.58f, 10.01f, 20.73f,
        10.55f, 20.85f, 11.09f, 20.93f, 11.64f, 20.98f, 12.19f, 21f, 12.74f, 20.98f, 13.29f, 20.93f,
        13.83f, 20.85f, 14.37f, 20.73f, 14.9f, 20.58f, 15.41f, 20.4f, 15.92f, 20.19f, 16.41f, 19.94f,
        16.89f, 19.67f, 17.35f, 19.37f, 17.79f, 19.04f, 18.21f, 18.69f, 18.61f, 18.31f, 18.98f, 17.9f,
        19.33f, 17.48f, 19.65f, 17.03f, 19.94f, 16.57f, 20.2f, 16.08f, 20.44f, 15.59f, 20.64f, 15.08f,
        20.81f, 14.56f, 20.3f, 14.8f, 19.76f, 15.01f, 19.21f, 15.17f, 18.65f, 15.29f, 18.08f, 15.37f,
        17.5f, 15.4f, 16.93f, 15.38f, 16.36f, 15.33f, 15.79f, 15.23f, 15.24f, 15.08f, 14.7f, 14.89f,
        14.17f, 14.66f, 13.66f, 14.39f, 13.18f, 14.09f, 12.72f, 13.74f, 12.29f, 13.36f, 11.89f, 12.95f,
        11.52f, 12.51f, 11.19f, 12.04f, 10.89f, 11.55f, 10.64f, 11.04f, 10.42f, 10.5f, 10.25f, 9.96f,
        10.12f, 9.4f, 10.03f, 8.83f, 9.99f, 8.26f, 9.99f, 7.68f, 10.04f, 7.11f, 10.13f, 6.54f,
        10.26f, 5.98f, 10.44f, 5.44f, 10.66f, 4.91f, 10.92f, 4.4f, 11.22f, 3.91f, 11.56f, 3.44f,
    };

    public static readonly float[] Keyhole =
    {
        7.63f, 14.73f, 7.44f, 14.6f, 7.27f, 14.45f, 7.11f, 14.28f, 6.98f, 14.09f, 6.88f, 13.89f,
        6.8f, 13.67f, 6.75f, 13.45f, 6.74f, 13.22f, 6.75f, 12.99f, 6.79f, 12.76f, 6.86f, 12.54f,
        6.96f, 12.33f, 7.09f, 12.14f, 7.24f, 11.97f, 7.41f, 11.81f, 7.6f, 11.69f, 7.81f, 11.58f,
        8.03f, 11.51f, 8.26f, 11.46f, 8.48f, 11.45f, 8.71f, 11.46f, 8.94f, 11.51f, 9.16f, 11.58f,
        9.36f, 11.69f, 9.56f, 11.81f, 9.73f, 11.97f, 9.88f, 12.14f, 10.01f, 12.33f, 10.11f, 12.54f,
        10.18f, 12.76f, 10.22f, 12.99f, 10.23f, 13.22f, 10.22f, 13.45f, 10.17f, 13.67f, 10.09f, 13.89f,
        9.99f, 14.09f, 9.86f, 14.28f, 9.7f, 14.45f, 9.53f, 14.6f, 9.33f, 14.73f, 9.93f, 17.6f,
        7.03f, 17.6f,
    };
}
