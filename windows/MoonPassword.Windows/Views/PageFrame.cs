using System.ComponentModel;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using MoonPassword.Windows.Update;
using Lumen.UI.Controls;

namespace MoonPassword.Windows.Views;

/// <summary>Pages that have a search box: Ctrl+F focuses it.</summary>
internal interface ISearchablePage
{
    void FocusSearch();
}

/// <summary>
/// The state behind the banners every page shows on top: a problem the shell wants the user to see and
/// an available / downloaded update. One instance; each <see cref="PageFrame"/> listens to it.
/// </summary>
internal sealed class BannerModel : INotifyPropertyChanged
{
    private readonly ShellController _shell;
    private string _dismissedUpdate = "";

    public BannerModel(ShellController shell) => _shell = shell;

    public event PropertyChangedEventHandler? PropertyChanged;

    public string? Warning { get; private set; }

    public bool ShowUpdate { get; private set; }

    public string UpdateTitle { get; private set; } = "";

    public string UpdateText { get; private set; } = "";

    public string UpdateButton { get; private set; } = "";

    public bool UpdateMandatory { get; private set; }

    private string UpdateKey => _shell.Update.State + ":" + _shell.Update.LatestName;

    public void Refresh()
    {
        Warning = _shell.BannerWarning;
        var u = _shell.Update;
        ShowUpdate = UpdateView.IsActionable(u) && (u.Mandatory || UpdateKey != _dismissedUpdate);
        UpdateMandatory = u.Mandatory;
        UpdateTitle = u.State == "ready" ? Strings.UpdateReadyTitle : Strings.UpdateAvailableTitle;
        UpdateText = (u.State == "ready" ? Strings.UpdateBannerReady(u.LatestName) : Strings.UpdateBannerAvailable(u.LatestName))
                     + (u.Mandatory ? Strings.UpdateMandatorySuffix : "");
        UpdateButton = UpdateView.ButtonText(u);
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(null));
    }

    /// <summary>"Not now": hidden until another version (or state) comes along. Mandatory updates stay.</summary>
    public void DismissUpdate()
    {
        _dismissedUpdate = UpdateKey;
        Refresh();
    }

    public async Task RunUpdateActionAsync(Visual anchor)
    {
        var action = _shell.Update.ButtonAction;
        if (action == UpdateInfo.Action.None) return;
        var error = await _shell.UpdateActionAsync(action);
        if (error is null) return;
        if (action == UpdateInfo.Action.Install) await Dialog.AlertAsync(Strings.UpdatePrepareFailedTitle, error, Strings.BtnOk, anchor);
        else _shell.ShowStatus(error, StatusKind.Error);
    }

    public void OpenDiagnostics() => _shell.ShowMainWindow(MainPage.Diagnostics);
}

/// <summary>A page with the shared banners above it.</summary>
internal sealed class PageFrame : UserControl
{
    private readonly BannerModel _model;
    private readonly InfoBar _warning;
    private readonly InfoBar _update;
    private readonly Button _updateButton;
    private readonly StackPanel _strip;

    public PageFrame(Control page, BannerModel model)
    {
        _model = model;
        Page = page;

        var diagnostics = new Button { Content = Strings.BannerDiagnose };
        diagnostics.Classes.Add("sm");
        diagnostics.Click += (_, _) => _model.OpenDiagnostics();
        _warning = new InfoBar { Severity = Severity.Warning, Actions = diagnostics, IsOpen = false };

        _updateButton = new Button();
        _updateButton.Classes.Add("primary");
        _updateButton.Classes.Add("sm");
        _updateButton.Click += async (_, _) =>
        {
            _updateButton.IsEnabled = false;
            try { await _model.RunUpdateActionAsync(this); }
            finally { _updateButton.IsEnabled = true; }
        };
        _update = new InfoBar { Severity = Severity.Info, Actions = _updateButton, IsClosable = true, IsOpen = false };
        _update.Closed += (_, _) => _model.DismissUpdate();

        _strip = new StackPanel { Spacing = 8, Margin = new Thickness(28, 16, 28, 0) };
        _strip.Children.Add(_warning);
        _strip.Children.Add(_update);

        var grid = new Grid { RowDefinitions = new RowDefinitions("Auto,*") };
        grid.Children.Add(_strip);
        Grid.SetRow(page, 1);
        grid.Children.Add(page);
        Content = grid;

        model.PropertyChanged += (_, _) => Apply();
        Apply();
    }

    public Control Page { get; }

    private void Apply()
    {
        _warning.Message = _model.Warning;
        _warning.IsOpen = _model.Warning is not null;
        _update.Title = _model.UpdateTitle;
        _update.Message = _model.UpdateText;
        _update.Severity = _model.UpdateMandatory ? Severity.Warning : Severity.Info;
        _update.IsClosable = !_model.UpdateMandatory;
        _update.IsOpen = _model.ShowUpdate;
        _updateButton.Content = _model.UpdateButton;
        _strip.IsVisible = _warning.IsOpen || _update.IsOpen;
    }
}
