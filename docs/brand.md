# MoonPassword 品牌标识

- **图形**：一弯新月，月身上挖出一个钥匙孔，表示“月”（moonl.cloud 家族）和“密码”。
- **颜色**：背景从靛蓝 `#312E81` 渐变到深靛蓝 `#1E1B4B`（上到下）；月亮为暖白 `#F8F4E3`。
- **来源**：图形由 `scripts/brand.py` 按几何参数生成（两个圆相减得到月牙，钥匙孔按奇偶规则挖空），三端的图标都从同一份数据来：
  - Android：`res/drawable/ic_launcher_foreground.xml`、`ic_launcher_background.xml`（自适应图标，同时作单色主题图标）和状态栏图标 `ic_stat_app.xml`；
  - Windows：`Logic/BrandMark.cs` 的多边形，窗口图标（`Views/AppIconImage.cs`）和托盘图标（`Services/TrayService.cs`）在运行时绘制，仓库里不放二进制图标；
  - macOS：`Assets/icon.png`（256 px）和菜单栏模板图 `Assets/tray.png`（44 px，黑色加透明度）。
- 修改图形时只改 `scripts/brand.py` 的参数，再运行 `python3 scripts/brand.py --emit`，不要单独手改某一端的文件。
