using System.ComponentModel;
using Avalonia.Controls;
using MoonPassword.Windows.Services;
using Lumen.UI.Controls;

namespace MoonPassword.Windows.Views;

/// <summary>
/// A sidebar entry with a live count badge and a string icon key. Lumen's <see cref="NavItem"/> is a plain class
/// (its Badge never refreshes), so the sidebar template in App.axaml binds these properties instead.
/// </summary>
internal sealed class NavPage : NavItem, INotifyPropertyChanged
{
    private int _count;

    public NavPage(MainPage page, string title, string iconKey, Control view)
    {
        Page = page;
        Title = title;
        IconKey = iconKey;
        View = view;
        Tag = page;
        PageFactory = () => view;
    }

    private NavPage(string header)
    {
        Title = header;
        IsHeader = true;
        IconKey = "";
    }

    /// <summary>A group label (not selectable).</summary>
    public static NavPage Group(string title) => new(title);

    public event PropertyChangedEventHandler? PropertyChanged;

    public MainPage Page { get; }

    public string IconKey { get; }

    public Control? View { get; }

    /// <summary>Shown as a badge (expanded sidebar) or a dot (collapsed); 0 hides it.</summary>
    public int Count
    {
        get => _count;
        set
        {
            if (_count == value) return;
            _count = value;
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(Count)));
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(CountText)));
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(HasCount)));
        }
    }

    public string CountText => _count > 99 ? "99+" : _count.ToString(System.Globalization.CultureInfo.InvariantCulture);

    public bool HasCount => _count > 0;
}
